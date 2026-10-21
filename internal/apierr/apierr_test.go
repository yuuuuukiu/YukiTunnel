package apierr

import (
	"errors"
	"net/http"
	"testing"
)

func TestNewAndError(t *testing.T) {
	e := New(http.StatusBadRequest, "BAD_THING", "出了点问题")
	if e.Status != 400 || e.Code != "BAD_THING" {
		t.Fatalf("unexpected error: %+v", e)
	}
	if e.Error() == "" {
		t.Fatal("Error() should not be empty")
	}
}

func TestConstructors(t *testing.T) {
	cases := []struct {
		err  *Error
		want int
	}{
		{NotFound("X", "m"), 404},
		{BadRequest("X", "m"), 400},
		{Unauthorized("X", "m"), 401},
		{Forbidden("X", "m"), 403},
		{TooMany("m"), 429},
		{Internal("m"), 500},
	}
	for _, c := range cases {
		if c.err.Status != c.want {
			t.Errorf("%s: status %d, want %d", c.err.Code, c.err.Status, c.want)
		}
	}
}

func TestAsUnwrapsAndWraps(t *testing.T) {
	// A plain error becomes a 500 with a generic code.
	plain := errors.New("boom")
	if got := As(plain); got.Status != 500 {
		t.Fatalf("plain error should map to 500, got %d", got.Status)
	}
	// An *Error passes through unchanged.
	orig := NotFound("NOPE", "not here")
	if got := As(orig); got.Code != "NOPE" {
		t.Fatalf("As lost the original code: %s", got.Code)
	}
	// Wrapf keeps the status but prefixes the message.
	wrapped := Wrapf(orig, "读取失败")
	if wrapped.Status != 404 {
		t.Fatalf("Wrapf changed the status: %d", wrapped.Status)
	}
	if wrapped.Message == orig.Message {
		t.Fatal("Wrapf did not add context")
	}
}

func TestAsNilSafe(t *testing.T) {
	if got := As(nil); got == nil {
		t.Fatal("As(nil) should still return an error object")
	}
}
