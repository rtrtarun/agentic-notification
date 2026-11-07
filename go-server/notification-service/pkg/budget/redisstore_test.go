package budget

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

// scripter is a fake Redis that answers every script call with a prepared reply.
type scripter struct {
	reply any
	err   error
	args  []any
}

func (s *scripter) result(ctx context.Context) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if s.err != nil {
		cmd.SetErr(s.err)
	} else {
		cmd.SetVal(s.reply)
	}
	return cmd
}

func (s *scripter) Eval(ctx context.Context, _ string, _ []string, args ...any) *redis.Cmd {
	s.args = args
	return s.result(ctx)
}
func (s *scripter) EvalSha(ctx context.Context, _ string, _ []string, args ...any) *redis.Cmd {
	s.args = args
	return s.result(ctx)
}
func (s *scripter) EvalRO(ctx context.Context, _ string, _ []string, args ...any) *redis.Cmd {
	return s.result(ctx)
}
func (s *scripter) EvalShaRO(ctx context.Context, _ string, _ []string, args ...any) *redis.Cmd {
	return s.result(ctx)
}
func (s *scripter) ScriptExists(ctx context.Context, _ ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceCmd(ctx)
}
func (s *scripter) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return redis.NewStringCmd(ctx)
}

func TestRedisStoreReadsTheScriptReply(t *testing.T) {
	ctx := context.Background()
	sc := &scripter{reply: []any{int64(1), int64(3), int64(0)}}
	allowed, used, oldest, err := NewRedisStore(sc).Slide(ctx, "k", 1000, 3600000, 5, "m")
	if err != nil || !allowed || used != 3 || oldest != 0 {
		t.Fatalf("allowed reply = %v %d %d %v", allowed, used, oldest, err)
	}
	if len(sc.args) != 4 || sc.args[2] != 5 || sc.args[3] != "m" {
		t.Fatalf("script arguments = %v", sc.args)
	}

	sc.reply = []any{int64(0), int64(5), int64(1234)}
	allowed, used, oldest, err = NewRedisStore(sc).Slide(ctx, "k", 1000, 3600000, 5, "m")
	if err != nil || allowed || used != 5 || oldest != 1234 {
		t.Fatalf("refused reply = %v %d %d %v", allowed, used, oldest, err)
	}
}

func TestRedisStoreRejectsUnexpectedReplies(t *testing.T) {
	ctx := context.Background()
	for name, sc := range map[string]*scripter{
		"redis error":  {err: errors.New("LOADING")},
		"short reply":  {reply: []any{int64(1)}},
		"wrong types":  {reply: []any{"1", "2", "3"}},
		"not an array": {reply: int64(1)},
	} {
		if _, _, _, err := NewRedisStore(sc).Slide(ctx, "k", 1, 1, 1, "m"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRedisBudgetOverTheRealAdapterFailsOpenOnAnError(t *testing.T) {
	b := NewRedisBudget(NewRedisStore(&scripter{err: errors.New("down")}), 3, 0)
	if r, err := b.Take(context.Background(), "s"); err == nil || r.Allowed {
		t.Fatalf("a store error must surface: %+v %v", r, err)
	}
}
