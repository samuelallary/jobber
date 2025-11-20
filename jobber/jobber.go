// Package jobber retrieves job offers from linedin based on query
// parameters and store the queries and the job offers on the database.
package jobber

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Alvaroalonsobabbel/jobber/db"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Jobber struct {
	ctx      context.Context
	linkedIn *linkedIn
	logger   *slog.Logger
	db       *db.Queries
	wg       sync.WaitGroup
}

func New(ctx context.Context, log *slog.Logger, db *db.Queries) (*Jobber, func()) {
	j := &Jobber{
		ctx:      ctx,
		linkedIn: NewLinkedIn(log),
		logger:   log,
		db:       db,
		wg:       sync.WaitGroup{},
	}

	// Schedule the existing queries upon start.
	queries, err := j.db.ListQueries(ctx)
	if err != nil {
		log.Error("failed to list queries in jobber.New", slog.String("error", err.Error()))
	}
	for _, q := range queries {
		go j.scheduleQuery(q)
	}
	return j, j.wg.Wait // TODO: improve flaky shutdown strategy.
}

func (j *Jobber) CreateQuery(keywords, location string) (*db.Query, error) {
	query, err := j.db.CreateQuery(j.ctx, &db.CreateQueryParams{
		Keywords: keywords,
		Location: location,
	})
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		// If the query exist we return the existing query.
		eq, err := j.db.GetQuery(j.ctx, &db.GetQueryParams{
			Keywords: keywords,
			Location: location,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get query: %w", err)
		}
		return eq, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create query: %w", err)
	}
	j.logger.Info("created new query",
		slog.Int64("queryID", query.ID),
		slog.String("keywords", keywords),
		slog.String("location", location),
	)

	// After creating a new query we run it so the feed has initial data.
	// In the frontend we use a spinner with htmx while this is being processed.
	j.runQuery(query)

	return query, nil
}

func (j *Jobber) ListOffers(keywords, location string) ([]*db.Offer, error) {
	q, err := j.db.GetQuery(j.ctx, &db.GetQueryParams{
		Keywords: keywords,
		Location: location,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get query: %w", err)
	}
	if err := j.db.UpdateQueryQAT(j.ctx, q.ID); err != nil {
		j.logger.Error("unable to update query timestamp", slog.Int64("queryID", q.ID), slog.String("error", err.Error()))
	}
	return j.db.ListOffers(j.ctx, &db.ListOffersParams{
		ID:       q.ID,
		PostedAt: time.Now().AddDate(0, 0, -7), // List offers posted in the last 7 days.
	})
}

func (j *Jobber) runQuery(q *db.Query) {
	// We remove queries that haven't been used for longer than 7 days.
	if time.Since(q.QueriedAt) > time.Hour*24*7 {
		if err := j.db.DeleteQuery(j.ctx, q.ID); err != nil {
			j.logger.Error("unable to delete query in jobber.runQuery", slog.Int64("queryID", q.ID), slog.String("error", err.Error()))
			return
		}
		j.logger.Info("deleting unused query", slog.Int64("queryID", q.ID), slog.String("keywords", q.Keywords), slog.String("location", q.Location))
		return
	}

	// TODO: extend ctx to linkedIn.search
	offers, err := j.linkedIn.search(q)
	if err != nil {
		j.logger.Error("unable to perform linkedIn search in jobber.runQuery", slog.Int64("queryID", q.ID), slog.String("error", err.Error()))
	}
	if len(offers) > 0 {
		for _, o := range offers {
			if err := j.db.CreateOffer(j.ctx, &o); err != nil {
				j.logger.Error("unable to create offer in jobber.runQuery", slog.String("error", err.Error()))
				continue
			}
			if err := j.db.CreateQueryOfferAssoc(j.ctx, &db.CreateQueryOfferAssocParams{
				QueryID: q.ID,
				OfferID: o.ID,
			}); err != nil {
				j.logger.Error("unable to create query offer association in jobber.runQuery", slog.String("error", err.Error()))
			}
		}
	}
	if err := j.db.UpdateQueryUAT(j.ctx, q.ID); err != nil {
		j.logger.Error("unable to update query timestamp in jobber.runQuery", slog.Int64("queryID", q.ID), slog.String("error", err.Error()))
	}

	// Schedule the next run.
	go j.scheduleQuery(q.ID)
}

func (j *Jobber) scheduleQuery(qID int64) {
	j.wg.Add(1)
	defer j.wg.Done()

	q, err := j.db.GetQueryByID(j.ctx, qID)
	if err != nil {
		j.logger.Error("unable to get query in jobber.scheduleQuery", slog.Int64("queryID", qID), slog.String("error", err.Error()))
		return
	}

	// delay := time.Hour
	// if q.UpdatedAt.Valid {
	// 	minuteOffset := time.Duration(q.UpdatedAt.Time.Minute()) * time.Minute
	// 	now := time.Now()
	// 	nextRun := now.Truncate(time.Hour).Add(time.Hour).Add(minuteOffset)
	// 	delay = nextRun.Sub(now)
	// }
	delay := 2 * time.Minute

	j.logger.Info("scheduling query", slog.Int64("queryID", q.ID), slog.Time("at", time.Now().Add(delay)))

	select {
	case <-time.After(delay):
		j.runQuery(q)
	case <-j.ctx.Done():
		j.logger.Info("scheduled query cancelled in jobber.scheduleQuery", slog.Int64("queryID", q.ID))
		return
	}
}
