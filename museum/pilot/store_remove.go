package pilot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pghq/go-museum/museum/internal"
	"github.com/pghq/go-museum/museum/store"
)

var (
	_ store.Remove = NewRemove(nil)
)

func (s *Store) Remove() store.Remove {
	s.t.Helper()
	res := s.Call(s.t)
	if len(res) != 1 {
		s.fail(s.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		s.fail(s.t, "unexpected type of return value")
		return nil
	}

	return remove
}

// Remove is a mock store.Remove
type Remove struct {
	internal.Mock
	t    *testing.T
	fail func(v ...interface{})
}

func (r *Remove) Statement() (string, []interface{}, error) {
	r.t.Helper()
	res := r.Call(r.t)
	if len(res) != 3 {
		r.fail(r.t, "unexpected length of return values")
		return "", nil, nil
	}

	if res[2] != nil {
		err, ok := res[2].(error)
		if !ok {
			r.fail(r.t, "unexpected type of return value")
			return "", nil, nil
		}
		return "", nil, err
	}

	statement, ok := res[0].(string)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return "", nil, nil
	}

	if res[1] != nil {
		args, ok := res[1].([]interface{})
		if !ok {
			r.fail(r.t, "unexpected type of return value")
			return "", nil, nil
		}
		return statement, args, nil
	}

	return statement, nil, nil
}

func (r *Remove) Filter(filter store.Filter) store.Remove {
	r.t.Helper()
	res := r.Call(r.t, filter)
	if len(res) != 1 {
		r.fail(r.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return nil
	}

	return remove
}

func (r *Remove) Order(by string) store.Remove {
	r.t.Helper()
	res := r.Call(r.t, by)
	if len(res) != 1 {
		r.fail(r.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return nil
	}

	return remove
}

func (r *Remove) First(first int) store.Remove {
	r.t.Helper()
	res := r.Call(r.t, first)
	if len(res) != 1 {
		r.fail(r.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return nil
	}

	return remove
}

func (r *Remove) After(key string, value interface{}) store.Remove {
	r.t.Helper()
	res := r.Call(r.t, key, value)
	if len(res) != 1 {
		r.fail(r.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return nil
	}

	return remove
}

func (r *Remove) Execute(ctx context.Context) (int, error) {
	r.t.Helper()
	res := r.Call(r.t, ctx)
	if len(res) != 2 {
		r.fail(r.t, "unexpected length of return values")
		return 0, nil
	}

	if res[1] != nil {
		err, ok := res[1].(error)
		if !ok {
			r.fail(r.t, "unexpected type of return value")
			return 0, nil
		}
		return 0, err
	}

	count, ok := res[0].(int)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return 0, nil
	}

	return count, nil
}

func (r *Remove) From(collection string) store.Remove {
	r.t.Helper()
	res := r.Call(r.t, collection)
	if len(res) != 1 {
		r.fail(r.t, "unexpected length of return values")
		return nil
	}

	remove, ok := res[0].(store.Remove)
	if !ok {
		r.fail(r.t, "unexpected type of return value")
		return nil
	}

	return remove
}

// NewRemove creates a mock store.Remove
func NewRemove(t *testing.T) *Remove {
	r := Remove{
		t: t,
	}

	if t != nil {
		r.fail = t.Fatal
	}

	return &r
}

// NewRemoveWithFail creates a mock store.Remove with an expected failure
func NewRemoveWithFail(t *testing.T, expect ...interface{}) *Remove {
	r := NewRemove(t)
	r.fail = func(v ...interface{}) {
		t.Helper()
		assert.Equal(t, append([]interface{}{t}, expect...), v)
	}

	return r
}
