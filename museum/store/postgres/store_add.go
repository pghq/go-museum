package postgres

import (
	"context"

	"github.com/Masterminds/squirrel"

	"github.com/pghq/go-museum/museum/diagnostic/errors"
	"github.com/pghq/go-museum/museum/store"
)

// Add creates an add command for the database.
func (s *Store) Add() store.Add {
	return NewAdd(s)
}

// Add is an instance of the add repository command using Postgres.
type Add struct {
	store   *Store
	opts []func(builder squirrel.InsertBuilder) squirrel.InsertBuilder
}

func (a *Add) To(collection string) store.Add {
	a.opts = append(a.opts, func(builder squirrel.InsertBuilder) squirrel.InsertBuilder {
		return builder.Into(collection)
	})

	return a
}

func (a *Add) Item(snapshot map[string]interface{}) store.Add {
	a.opts = append(a.opts, func(builder squirrel.InsertBuilder) squirrel.InsertBuilder {
		return builder.SetMap(snapshot)
	})

	return a
}

func (a *Add) Query(q store.Query) store.Add {
	if q, ok := q.(*Query); ok {
		s := squirrel.StatementBuilder.
			PlaceholderFormat(squirrel.Dollar).
			Select()
		for _, opt := range q.opts {
			s = opt(s)
		}

		a.opts = append(a.opts, func(builder squirrel.InsertBuilder) squirrel.InsertBuilder {
			return builder.Select(s)
		})
	}

	return a
}

func (a *Add) Execute(ctx context.Context) (int, error) {
	sql, args, err := a.Statement()
	if err != nil {
		return 0, errors.BadRequest(err)
	}

	tag, err := a.store.pool.Exec(ctx, sql, args...)
	if err != nil {
		if IsIntegrityConstraintViolation(err) {
			return 0, errors.BadRequest(err)
		}
		return 0, errors.Wrap(err)
	}

	return int(tag.RowsAffected()), nil
}

func (a *Add) Statement() (string, []interface{}, error) {
	builder := squirrel.StatementBuilder.
		PlaceholderFormat(squirrel.Dollar).
		Insert("")

	for _, opt := range a.opts {
		builder = opt(builder)
	}

	return builder.ToSql()
}

// NewAdd creates a new add command for the Postgres database.
func NewAdd(store *Store) *Add {
	a := Add{store: store}
	return &a
}
