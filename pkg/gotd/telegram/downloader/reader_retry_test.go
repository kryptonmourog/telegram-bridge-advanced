package downloader

import (
	"context"
	"testing"

	"github.com/go-faster/errors"

	"go.mau.fi/mautrix-telegram/pkg/gotd/pool"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// brokenConnSchema fails the first requests the way a broken connection does.
type brokenConnSchema struct {
	failures int
	err      error
	calls    int
}

func (s *brokenConnSchema) Chunk(ctx context.Context, offset int64, limit int) (chunk, error) {
	s.calls++
	if s.calls <= s.failures {
		return chunk{}, s.err
	}
	return chunk{data: make([]byte, limit)}, nil
}

func (s *brokenConnSchema) Hashes(ctx context.Context, offset int64) ([]tg.FileHash, error) {
	return nil, nil
}

func TestReaderRetriesAfterBrokenConnection(t *testing.T) {
	for _, err := range []error{
		errors.Wrap(pool.ErrConnDead, "waitSession"),
		errors.Wrap(context.Canceled, "engine forcibly closed"),
	} {
		sch := &brokenConnSchema{failures: 2, err: err}
		b, gotErr := plainReader(sch, 16).Next(context.Background())
		if gotErr != nil || len(b.data) != 16 || sch.calls != 3 {
			t.Fatalf("%v: got %v after %d calls", err, gotErr, sch.calls)
		}
	}
}

func TestReaderGivesUpOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sch := &brokenConnSchema{failures: 100, err: context.Canceled}
	if _, err := plainReader(sch, 16).Next(ctx); err == nil || sch.calls != 1 {
		t.Fatalf("expected no retry, got %v after %d calls", err, sch.calls)
	}
}
