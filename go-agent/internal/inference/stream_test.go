package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamBackpressurePreservesEveryToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 100; i++ {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := NewOllamaClient(s.URL, time.Second).ChatStream(ctx, json.RawMessage(`{"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var deltas strings.Builder
	var final string
	done := false
	for chunk := range stream {
		if chunk.Error != nil {
			t.Fatal(chunk.Error)
		}
		if chunk.Done {
			done = true
			final = chunk.Text
		} else {
			deltas.WriteString(chunk.Text)
			time.Sleep(time.Millisecond)
		}
	}
	if deltas.Len() != 100 || final != strings.Repeat("x", 100) || !done {
		t.Fatalf("lost output: deltas=%d final=%d done=%v", deltas.Len(), len(final), done)
	}
}

func TestStreamReportsPrematureEnd(t *testing.T) {
	for _, data := range []string{"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "data: not-json\n\n", "data: " + strings.Repeat("x", 4*1024*1024+1) + "\n\n"} {
		t.Run(fmt.Sprint(len(data)), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, data) }))
			defer s.Close()
			stream, err := NewOllamaClient(s.URL, time.Second).ChatStream(context.Background(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for chunk := range stream {
				if chunk.Error != nil {
					failed = true
				}
				if chunk.Done {
					t.Error("incomplete stream reported success")
				}
			}
			if !failed {
				t.Fatal("incomplete or malformed stream silently succeeded")
			}
		})
	}
}

func TestStreamCancellationUnblocksSlowConsumer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 100; i++ {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		}
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewOllamaClient(s.URL, time.Second).ChatStream(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	ended := make(chan struct{})
	go func() {
		for range stream {
		}
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("cancelled stream did not terminate")
	}
}
