package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgproto3/v2"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/stretchr/testify/assert"

	"github.com/pghq/go-museum/museum/diagnostic/errors"
	"github.com/pghq/go-museum/museum/diagnostic/log"
	"github.com/pghq/go-museum/museum/internal"
	"github.com/pghq/go-museum/museum/pilot"
)

var (
	_ Pool     = NewPostgresPool(nil)
	_ pgx.Tx   = NewPostgresTx(nil)
	_ pgx.Rows = NewPostgresRows(nil)
)

func TestStore(t *testing.T) {
	t.Run("can create new database", func(t *testing.T) {
		dsn := "postgres://postgres:postgres@db:5432"
		s := NewStore(dsn)
		assert.Equal(t, dsn, s.primaryDSN)
		assert.NotNil(t, dsn, s.secondaryDSN)
		assert.Equal(t, DefaultSQLMaxOpenConns, s.maxConns)
		assert.Equal(t, time.Duration(0), s.maxConnLifetime)
	})

	s := NewStore("postgres://postgres:postgres@db:5432")
	t.Run("can set max connection lifetime", func(t *testing.T) {
		lifetime := time.Second
		s = s.MaxConnLifetime(lifetime)
		assert.NotNil(t, s)
		assert.Equal(t, lifetime, s.maxConnLifetime)
	})

	t.Run("can set max connections", func(t *testing.T) {
		conns := 5
		s = s.MaxConns(conns)
		assert.NotNil(t, s)
		assert.Equal(t, conns, s.maxConns)
	})

	t.Run("can connect", func(t *testing.T) {
		log.Writer(io.Discard)
		s := NewStore("")
		err := s.Connect()
		assert.NotNil(t, err)
		assert.False(t, s.IsConnected())
	})

	t.Run("can not set a bad secondary", func(t *testing.T) {
		s := NewStore("postgres://postgres:postgres@db:5432")
		s.connect = func(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
			if config.ConnString() == "secondary" {
				return nil, errors.New("bad secondary")
			}
			return &pgxpool.Pool{}, nil
		}
		err := s.Secondary("secondary").Connect()
		assert.NotNil(t, err)
		assert.False(t, s.IsConnected())
	})

	t.Run("can set secondary database client", func(t *testing.T) {
		s, _, _ := setup(t)
		assert.NotNil(t, s.pool)
		assert.NotNil(t, s.secondary)
		assert.True(t, s.IsConnected())
	})

	t.Run("raises sql open errors on migration", func(t *testing.T) {
		s := NewStore("postgres://postgres:postgres@db:5432").
			Migrations(embed.FS{}, "migrations")
		s.connect = func(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
			return &pgxpool.Pool{}, nil
		}
		s.migrations.open = func(driverName, dataSourceName string) (*sql.DB, error) {
			return &sql.DB{}, errors.New("an error has occurred")
		}
		assert.NotNil(t, s)

		err := s.Connect()
		assert.NotNil(t, err)
	})

	t.Run("raises migration errors", func(t *testing.T) {
		s := NewStore("postgres://postgres:postgres@db:5432").
			Migrations(embed.FS{}, "migrations")
		s.connect = func(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
			return &pgxpool.Pool{}, nil
		}
		s.migrations.open = func(driverName, dataSourceName string) (*sql.DB, error) {
			return sql.OpenDB(ErrConnector{}), nil
		}
		assert.NotNil(t, s)

		err := s.Connect()
		assert.NotNil(t, err)
	})

	t.Run("can create a new cursor", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		c := NewCursor(rows)
		assert.NotNil(t, c)
	})

	t.Run("cursor can be closed", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		rows.Expect("Close")

		c := NewCursor(rows)
		defer c.Close()
	})

	t.Run("cursor handles decode errors", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		rows.Expect("Scan").
			Return(errors.New("an error has occurred"))

		c := NewCursor(rows)
		assert.NotNil(t, c)
		err := c.Decode()
		assert.NotNil(t, err)
	})

	t.Run("cursor can decode values", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		var one int
		rows.Expect("Scan", &one).
			Return(nil)
		rows.Expect("Err").
			Return(nil)

		c := NewCursor(rows)
		err := c.Decode(&one)
		assert.Nil(t, err)
		assert.Nil(t, c.Error())
	})

	t.Run("cursor keeps track of errors", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		rows.Expect("Err").
			Return(errors.New("an error has occurred"))

		c := NewCursor(rows)
		err := c.Error()
		assert.NotNil(t, err)
	})

	t.Run("cursor iterates through values", func(t *testing.T) {
		rows := NewPostgresRows(t)
		defer rows.Assert(t)

		rows.Expect("Next").
			Return(false)

		c := NewCursor(rows)
		c.Next()
	})

	t.Run("can recognize integrity violation errors", func(t *testing.T) {
		err := &pgconn.PgError{Code: pgerrcode.IntegrityConstraintViolation}
		assert.True(t, IsIntegrityConstraintViolation(err))
	})

	t.Run("can distinguish non integrity violation errors", func(t *testing.T) {
		err := errors.New("an error has occurred")
		assert.False(t, IsIntegrityConstraintViolation(err))
	})

	t.Run("can send pgx logs", func(t *testing.T) {
		l := NewPGXLogger()
		var buf bytes.Buffer
		log.Writer(&buf)

		log.Level("debug")
		l.Log(context.TODO(), pgx.LogLevelDebug, "an error has occurred", nil)
		assert.True(t, strings.Contains(buf.String(), "debug"))

		buf.Reset()
		log.Level("info")
		l.Log(context.TODO(), pgx.LogLevelInfo, "an error has occurred", nil)
		assert.True(t, strings.Contains(buf.String(), "info"))

		buf.Reset()
		log.Level("warn")
		l.Log(context.TODO(), pgx.LogLevelWarn, "an error has occurred", nil)
		assert.True(t, strings.Contains(buf.String(), "warn"))

		buf.Reset()
		log.Level("error")
		l.Log(context.TODO(), pgx.LogLevelError, "an error has occurred", nil)
		assert.True(t, strings.Contains(buf.String(), "error"))
	})

	t.Run("can send goose logs", func(t *testing.T) {
		l := NewGooseLogger()
		var buf bytes.Buffer
		log.Writer(&buf)
		log.Level("info")

		l.Print("an error has occurred")
		assert.True(t, strings.Contains(buf.String(), "an error has occurred"))

		buf.Reset()
		l.Printf("an %s has occurred", "error")
		assert.True(t, strings.Contains(buf.String(), "an error has occurred"))

		buf.Reset()
		l.Println("an error has occurred")
		assert.True(t, strings.Contains(buf.String(), "an error has occurred"))

		buf.Reset()
		l.Fatal("an error has occurred")
		assert.True(t, strings.Contains(buf.String(), "an error has occurred"))

		buf.Reset()
		l.Fatalf("an %s has occurred", "error")
		assert.True(t, strings.Contains(buf.String(), "an error has occurred"))
	})
}

func TestStore_Add(t *testing.T) {
	t.Run("can create new instance", func(t *testing.T) {
		s, _, _ := setup(t)
		assert.NotNil(t, s.Add())
	})

	t.Run("raises bad request errors on execute", func(t *testing.T) {
		s, _, _ := setup(t)
		add := NewAdd(s)

		_, err := add.Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises fatal errors on execute", func(t *testing.T) {
		s, primary, _ := setup(t)
		add := NewAdd(s)

		primary.Expect("Exec", context.TODO(), "INSERT INTO tests (coverage) VALUES ($1)", 50).
			Return(nil, errors.New("an error has occurred"))
		defer primary.Assert(t)

		_, err := add.
			To("tests").
			Item(map[string]interface{}{"coverage": 50}).
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("raises integrity errors on execute", func(t *testing.T) {
		s, primary, _ := setup(t)
		add := NewAdd(s)

		primary.Expect("Exec", context.TODO(), "INSERT INTO tests (coverage) VALUES ($1)", 50).
			Return(nil, &pgconn.PgError{Code: pgerrcode.IntegrityConstraintViolation})
		defer primary.Assert(t)

		_, err := add.
			To("tests").
			Item(map[string]interface{}{"coverage": 50}).
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("can execute", func(t *testing.T) {
		s, primary, _ := setup(t)
		add := NewAdd(s)

		primary.Expect("Exec", context.TODO(), "INSERT INTO tests (coverage) SELECT coverage FROM units LIMIT 1").
			Return(pgconn.CommandTag{}, nil)
		defer primary.Assert(t)

		_, err := add.
			Item(map[string]interface{}{"coverage": 0}).
			Query(s.Query().From("units").Return("coverage").First(1)).
			To("tests").
			Execute(context.TODO())
		assert.Nil(t, err)
	})
}

func TestStore_Query(t *testing.T) {
	t.Run("can create new instance", func(t *testing.T) {
		s, _, _ := setup(t)
		assert.NotNil(t, s.Query())
	})

	t.Run("raises bad request errors", func(t *testing.T) {
		s, _, _ := setup(t)
		query := NewQuery(s)

		_, err := query.Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises no content errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		query := NewQuery(s)

		primary.Expect("Query", context.TODO(), "SELECT coverage FROM tests").
			Return(nil, pgx.ErrNoRows)
		defer primary.Assert(t)

		_, err := query.
			From("tests").
			Return("coverage").
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises fatal errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		query := NewQuery(s)

		primary.Expect("Query", context.TODO(), "SELECT coverage FROM tests").
			Return(nil, errors.New("an error has occurred"))
		defer primary.Assert(t)

		_, err := query.
			From("tests").
			Return("coverage").
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can execute on primary", func(t *testing.T) {
		s, primary, _ := setup(t)
		query := NewQuery(s)

		primary.Expect("Query", context.TODO(), "SELECT runs FROM tests JOIN units ON runs.id = units.id WHERE coverage > $1 AND id >= $2 ORDER BY coverage DESC LIMIT 5", 50, 2).
			Return(NewPostgresRows(t), nil)
		defer primary.Assert(t)

		_, err := query.From("tests").
			And("units ON runs.id = units.id").
			Filter(s.Filter().Gt("coverage", 50)).
			Order("coverage DESC").
			Return("runs").
			First(5).
			After("id", 2).
			Execute(context.TODO())
		assert.Nil(t, err)
	})

	t.Run("can execute on secondary", func(t *testing.T) {
		s, _, secondary := setup(t)
		query := NewQuery(s)
		secondary.Expect("Query", context.TODO(), "SELECT runs FROM tests").
			Return(NewPostgresRows(t), nil)
		defer secondary.Assert(t)

		_, err := query.
			Secondary().
			From("tests").
			Return("runs").
			Execute(context.TODO())
		assert.Nil(t, err)
	})
}

func TestStore_Remove(t *testing.T) {
	t.Run("can create new instance", func(t *testing.T) {
		s, _, _ := setup(t)
		assert.NotNil(t, s.Remove())
	})

	t.Run("raises bad request errors", func(t *testing.T) {
		s, _, _ := setup(t)
		remove := NewRemove(s)

		_, err := remove.Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises integrity errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		remove := NewRemove(s)

		primary.Expect("Exec", context.TODO(), "DELETE FROM tests").
			Return(nil, &pgconn.PgError{Code: pgerrcode.IntegrityConstraintViolation})
		defer primary.Assert(t)

		_, err := remove.
			From("tests").
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises fatal errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		remove := NewRemove(s)

		primary.Expect("Exec", context.TODO(), "DELETE FROM tests").
			Return(nil, errors.New("an error has occurred"))
		defer primary.Assert(t)

		_, err := remove.
			From("tests").
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can execute", func(t *testing.T) {
		s, primary, _ := setup(t)
		remove := NewRemove(s)

		primary.Expect("Exec", context.TODO(), "DELETE FROM tests WHERE coverage > $1 AND id >= $2 ORDER BY coverage DESC LIMIT 5", 50, 2).
			Return(pgconn.CommandTag{}, nil)
		defer primary.Assert(t)

		_, err := remove.
			From("tests").
			Filter(s.Filter().Gt("coverage", 50)).
			Order("coverage DESC").
			First(5).
			After("id", 2).
			Execute(context.TODO())
		assert.Nil(t, err)
	})
}

func TestStore_Transaction(t *testing.T) {
	t.Run("handles new transaction errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		primary.Expect("Begin", context.TODO()).
			Return(nil, errors.New("an error occurred"))
		defer primary.Assert(t)

		_, err := s.Transaction(context.TODO())
		assert.NotNil(t, err)
	})

	t.Run("can create new instance", func(t *testing.T) {
		s, primary, _ := setup(t)
		primary.Expect("Begin", context.TODO()).
			Return(NewPostgresTx(t), nil)
		defer primary.Assert(t)

		tx, err := s.Transaction(context.TODO())
		assert.Nil(t, err)
		assert.NotNil(t, tx)
	})

	t.Run("raises bad request errors", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		defer ptx.Assert(t)

		add := pilot.NewAdd(t)
		add.Expect("Statement").
			Return("", nil, errors.New("an error has occurred"))
		defer add.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		_, err := tx.Execute(add)
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises fatal errors", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Exec", context.TODO(), "").
			Return(0, errors.New("an error has occurred"))
		defer ptx.Assert(t)

		add := pilot.NewAdd(t)
		add.Expect("Statement").
			Return("", nil, nil)
		defer add.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		_, err := tx.Execute(add)
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can execute", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Exec", context.TODO(), "").
			Return(pgconn.CommandTag{}, nil)
		defer ptx.Assert(t)

		add := pilot.NewAdd(t)
		add.Expect("Statement").
			Return("", nil, nil)
		defer add.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		_, err := tx.Execute(add)
		assert.Nil(t, err)
	})

	t.Run("raises commit errors", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Commit", context.TODO()).
			Return(errors.New("an error has occurred"))
		defer ptx.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		err := tx.Commit()
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can commit", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Commit", context.TODO()).
			Return(nil)
		defer ptx.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		err := tx.Commit()
		assert.Nil(t, err)
	})

	t.Run("raises rollback errors", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Rollback", context.TODO()).
			Return(errors.New("an error has occurred"))
		defer ptx.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		err := tx.Rollback()
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can rollback", func(t *testing.T) {
		ptx := NewPostgresTx(t)
		ptx.Expect("Rollback", context.TODO()).
			Return(nil)
		defer ptx.Assert(t)

		tx := transaction{ctx: context.TODO(), tx: ptx}
		err := tx.Rollback()
		assert.Nil(t, err)
	})
}

func TestStore_Update(t *testing.T) {
	t.Run("can create new instance", func(t *testing.T) {
		s, _, _ := setup(t)
		assert.NotNil(t, s.Update())
	})

	t.Run("raises bad request errors", func(t *testing.T) {
		s, _, _ := setup(t)
		update := NewUpdate(s)

		_, err := update.Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises integrity errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		update := NewUpdate(s)

		primary.Expect("Exec", context.TODO(), "UPDATE tests SET coverage = $1", 0).
			Return(nil, &pgconn.PgError{Code: pgerrcode.IntegrityConstraintViolation})
		defer primary.Assert(t)

		_, err := update.
			In("tests").
			Item(map[string]interface{}{"coverage": 0}).
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises fatal errors", func(t *testing.T) {
		s, primary, _ := setup(t)
		update := NewUpdate(s)

		primary.Expect("Exec", context.TODO(), "UPDATE tests SET coverage = $1", 0).
			Return(nil, errors.New("an error has occurred"))
		defer primary.Assert(t)

		_, err := update.
			In("tests").
			Item(map[string]interface{}{"coverage": 0}).
			Execute(context.TODO())
		assert.NotNil(t, err)
		assert.True(t, errors.IsFatal(err))
	})

	t.Run("can execute", func(t *testing.T) {
		s, primary, _ := setup(t)
		update := NewUpdate(s)

		primary.Expect("Exec", context.TODO(), "UPDATE tests SET coverage = $1 WHERE coverage > $2", 0, 50).
			Return(pgconn.CommandTag{}, nil)
		defer primary.Assert(t)

		_, err := update.
			In("tests").
			Filter(s.Filter().Gt("coverage", 50)).
			Item(map[string]interface{}{"coverage": 0}).
			Execute(context.TODO())
		assert.Nil(t, err)
	})
}

func TestStore_Filter(t *testing.T) {
	t.Run("raises invalid slice type errors", func(t *testing.T) {
		f := Filter().
			Eq("key", []interface{}{}).
			Lt("key", []interface{}{}).
			Gt("key", []interface{}{}).
			NotEq("key", []interface{}{})

		_, _, err := squirrel.Select("column").From("tests").Where(f).ToSql()
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises invalid conjunction errors", func(t *testing.T) {
		f := Filter().
			Eq("eq", 1).
			Or(nil).
			And(nil)

		_, _, err := squirrel.Select("column").From("tests").Where(f).ToSql()
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("raises bad op errors", func(t *testing.T) {
		f := Filter().
			Lt("key", nil)

		_, _, err := squirrel.Select("column").From("tests").Where(f).ToSql()
		assert.NotNil(t, err)
		assert.False(t, errors.IsFatal(err))
	})

	t.Run("can sql-ize", func(t *testing.T) {
		or := Filter().Lt("lt", 2)
		and := Filter().Gt("gt", 3).
			NotEq("ne", 4).
			BeginsWith("prefix", "5").
			EndsWith("suffix", "6").
			Contains("containsString", "7").
			Contains("containsSlice", []interface{}{8, 9, 10}).
			Contains("containsNumber", 11).
			NotContains("notContainsString", "7").
			NotContains("notContainsSlice", []interface{}{8, 9, 10}).
			NotContains("notContainsNumber", 11)

		f := Filter().
			Eq("eq", 1).
			Or(or).
			And(and)

		sql, args, err := squirrel.Select("column").From("tests").Where(f).ToSql()
		assert.Nil(t, err)
		assert.Equal(t, "SELECT column FROM tests WHERE eq = ? AND (eq = ? OR lt < ?) AND (eq = ? AND (eq = ? OR lt < ?) AND gt > ? AND ne <> ? AND prefix LIKE ? AND suffix LIKE ? AND containsString LIKE ? AND containsSlice IN (?,?,?) AND containsNumber IN (?) AND notContainsString NOT LIKE ? AND notContainsSlice NOT IN (?,?,?) AND notContainsNumber NOT IN (?))", sql)
		assert.Equal(t, []interface{}{1, 1, 2, 1, 1, 2, 3, 4, "%5", "6%", "%7%", 8, 9, 10, 11, "%7%", 8, 9, 10, 11}, args)
	})
}

type PostgresPool struct {
	internal.Mock
	t *testing.T
}

func (p *PostgresPool) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	p.t.Helper()
	res := p.Call(p.t, append([]interface{}{ctx, sql}, args...)...)
	if len(res) != 2 {
		p.Fatalf(p.t, "length of return values for Exec is not equal to 2")
	}

	if res[1] != nil {
		err, ok := res[1].(error)
		if !ok {
			p.Fatalf(p.t, "return value #2 of Exec is not an error")
		}
		return nil, err
	}

	tag, ok := res[0].(pgconn.CommandTag)
	if !ok {
		p.Fatalf(p.t, "return value #1 of Exec is not a pgconn.CommandTag")
	}

	return tag, nil
}

func NewPostgresPool(t *testing.T) *PostgresPool {
	p := PostgresPool{t: t}

	return &p
}

func setup(t *testing.T) (*Store, *PostgresPool, *PostgresPool) {
	t.Helper()

	s := NewStore("postgres://postgres:postgres@db:5432")
	s.connect = func(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
		t.Helper()
		assert.NotNil(t, config)
		assert.IsType(t, &PGXLogger{}, config.ConnConfig.Logger)
		assert.Equal(t, int32(DefaultSQLMaxOpenConns), config.MaxConns)
		assert.Equal(t, time.Duration(0), config.MaxConnLifetime)
		assert.Equal(t, s.primaryDSN, config.ConnString())

		return &pgxpool.Pool{}, nil
	}
	err := s.Connect()
	assert.Nil(t, err)
	primary := NewPostgresPool(t)
	secondary := NewPostgresPool(t)
	s.pool = primary
	s.secondary = secondary

	return s, primary, secondary
}

func (p *PostgresPool) Begin(ctx context.Context) (pgx.Tx, error) {
	p.t.Helper()
	res := p.Call(p.t, ctx)
	if len(res) != 2 {
		p.Fatalf(p.t, "length of return values for Begin is not equal to 1")
	}

	if res[1] != nil {
		err, ok := res[1].(error)
		if !ok {
			p.Fatalf(p.t, "return value #2 of Begin is not an error")
		}
		return nil, err
	}

	tx, ok := res[0].(pgx.Tx)
	if !ok {
		p.Fatalf(p.t, "return value #1 of Begin is not a pgx.Tx")
	}

	return tx, nil
}

type PostgresTx struct {
	internal.Mock
	t *testing.T
}

func (tx *PostgresTx) Commit(ctx context.Context) error {
	tx.t.Helper()
	res := tx.Call(tx.t, ctx)
	if len(res) != 1 {
		tx.Fatalf(tx.t, "length of return values for Commit is not equal to 1")
	}

	if res[0] != nil {
		err, ok := res[0].(error)
		if !ok {
			tx.Fatalf(tx.t, "return value #1 of Commit is not an error")
		}
		return err
	}

	return nil
}

func (tx *PostgresTx) Rollback(ctx context.Context) error {
	tx.t.Helper()
	res := tx.Call(tx.t, ctx)
	if len(res) != 1 {
		tx.Fatalf(tx.t, "length of return values for Rollback is not equal to 1")
	}

	if res[0] != nil {
		err, ok := res[0].(error)
		if !ok {
			tx.Fatalf(tx.t, "return value #1 of Rollback is not an error")
		}
		return err
	}

	return nil
}

func (tx *PostgresTx) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	tx.t.Helper()
	res := tx.Call(tx.t, append([]interface{}{ctx, sql}, args...)...)
	if len(res) != 2 {
		tx.Fatalf(tx.t, "length of return values for Exec is not equal to 2")
	}

	if res[1] != nil {
		err, ok := res[1].(error)
		if !ok {
			tx.Fatalf(tx.t, "return value #2 of Exec is not an error")
		}
		return nil, err
	}

	tag, ok := res[0].(pgconn.CommandTag)
	if !ok {
		tx.Fatalf(tx.t, "return value #2 of Exec is not a pgconn.CommandTag")
	}

	return tag, nil
}

func (tx *PostgresTx) Begin(ctx context.Context) (pgx.Tx, error) {
	panic("not implemented")
}

func (tx *PostgresTx) BeginFunc(ctx context.Context, f func(pgx.Tx) error) (err error) {
	panic("implement me")
}

func (tx *PostgresTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	panic("implement me")
}

func (tx *PostgresTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	panic("implement me")
}

func (tx *PostgresTx) LargeObjects() pgx.LargeObjects {
	panic("implement me")
}

func (tx *PostgresTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	panic("implement me")
}

func (tx *PostgresTx) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	panic("implement me")
}

func (tx *PostgresTx) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	panic("implement me")
}

func (tx *PostgresTx) QueryFunc(ctx context.Context, sql string, args []interface{}, scans []interface{}, f func(pgx.QueryFuncRow) error) (pgconn.CommandTag, error) {
	panic("implement me")
}

func (tx *PostgresTx) Conn() *pgx.Conn {
	panic("implement me")
}

func NewPostgresTx(t *testing.T) *PostgresTx {
	tx := PostgresTx{t: t}

	return &tx
}

func (p *PostgresPool) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	p.t.Helper()
	res := p.Call(p.t, append([]interface{}{ctx, sql}, args...)...)
	if len(res) != 2 {
		p.Fatalf(p.t, "length of return values for Query is not equal to 2")
	}

	if res[1] != nil {
		err, ok := res[1].(error)
		if !ok {
			p.Fatalf(p.t, "return value #2 of Err is not an error")
		}
		return nil, err
	}

	rows, ok := res[0].(pgx.Rows)
	if !ok {
		p.Fatalf(p.t, "return value #1 of Query is not a pgx.Rows")
	}

	return rows, nil
}

type PostgresRows struct {
	internal.Mock
	t *testing.T
}

func (r *PostgresRows) Close() {
	r.t.Helper()
	res := r.Call(r.t)
	if len(res) != 0 {
		r.Fatalf(r.t, "length of return values for Close is not equal to 0")
	}
}

func (r *PostgresRows) Err() error {
	r.t.Helper()
	res := r.Call(r.t)
	if len(res) != 1 {
		r.Fatalf(r.t, "length of return values for Err is not equal to 1")
	}

	if res[0] != nil {
		err, ok := res[0].(error)
		if !ok {
			r.Fatalf(r.t, "return value #1 of Err is not an error")
		}
		return err
	}

	return nil
}

func (r *PostgresRows) Next() bool {
	r.t.Helper()
	res := r.Call(r.t)
	if len(res) != 1 {
		r.Fatalf(r.t, "length of return values for Next is not equal to 1")
	}

	next, ok := res[0].(bool)
	if !ok {
		r.Fatalf(r.t, "return value #1 of Next is not a bool")
	}

	return next
}

func (r *PostgresRows) Scan(dest ...interface{}) error {
	r.t.Helper()
	res := r.Call(r.t, dest...)
	if len(res) != 1 {
		r.Fatalf(r.t, "length of return values for Scan is not equal to 1")
	}

	if res[0] != nil {
		err, ok := res[0].(error)
		if !ok {
			r.Fatalf(r.t, "return value #1 of Scan is not an error")
		}
		return err
	}

	return nil
}

func (r *PostgresRows) CommandTag() pgconn.CommandTag {
	panic("implement me")
}

func (r *PostgresRows) FieldDescriptions() []pgproto3.FieldDescription {
	panic("implement me")
}

func (r *PostgresRows) Values() ([]interface{}, error) {
	panic("implement me")
}

func (r *PostgresRows) RawValues() [][]byte {
	panic("implement me")
}

func NewPostgresRows(t *testing.T) *PostgresRows {
	rows := PostgresRows{t: t}

	return &rows
}

type ErrConnector struct{}

func (e ErrConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return nil, errors.New("an error has occurred")
}

func (e ErrConnector) Driver() driver.Driver {
	panic("not imlemented")
}
