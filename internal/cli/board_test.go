package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

type boardInfoBackend struct {
	sdk.Backend
	info *pb.BackendInfo
	err  error
}

func (b boardInfoBackend) Describe(context.Context) (*pb.BackendInfo, error) {
	return b.info, b.err
}

func TestOpenBoardWeb(t *testing.T) {
	var opened string
	backend := boardInfoBackend{info: &pb.BackendInfo{BoardUrl: "https://example.com/board"}}
	url, err := openBoardWeb(context.Background(), backend, func(u string) error {
		opened = u
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://example.com/board" || opened != url {
		t.Fatalf("url = %q, opened = %q", url, opened)
	}
}

func TestOpenBoardWebWithoutURL(t *testing.T) {
	backend := boardInfoBackend{info: &pb.BackendInfo{}}
	if _, err := openBoardWeb(context.Background(), backend, func(string) error { return nil }); err == nil {
		t.Fatal("expected an error for a backend without a web board")
	}
}

func TestOpenBoardWebOpenError(t *testing.T) {
	backend := boardInfoBackend{info: &pb.BackendInfo{BoardUrl: "https://example.com/board"}}
	_, err := openBoardWeb(context.Background(), backend, func(string) error {
		return errors.New("no browser")
	})
	if err == nil || !strings.Contains(err.Error(), "https://example.com/board") {
		t.Fatalf("error = %v", err)
	}
}
