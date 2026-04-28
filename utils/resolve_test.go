package utils

import (
	"errors"
	"strings"
	"testing"
)

var errNotFound = errors.New("not found")

func TestResolveRef_ExactID(t *testing.T) {
	items := map[string]*int{"abc123": ptr(1), "def456": ptr(2)}
	names := map[string]string{}

	id, err := ResolveRef(items, names, "abc123", errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc123" {
		t.Errorf("got %q, want %q", id, "abc123")
	}
}

func TestResolveRef_ByName(t *testing.T) {
	items := map[string]*int{"abc123": ptr(1)}
	names := map[string]string{"myvm": "abc123"}

	id, err := ResolveRef(items, names, "myvm", errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc123" {
		t.Errorf("got %q, want %q", id, "abc123")
	}
}

func TestResolveRef_ByPrefix(t *testing.T) {
	items := map[string]*int{"abc123def": ptr(1), "xyz789": ptr(2)}
	names := map[string]string{}

	id, err := ResolveRef(items, names, "abc", errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc123def" {
		t.Errorf("got %q, want %q", id, "abc123def")
	}
}

func TestResolveRef_AmbiguousPrefix(t *testing.T) {
	items := map[string]*int{"abc111": ptr(1), "abc222": ptr(2)}
	names := map[string]string{}

	_, err := ResolveRef(items, names, "abc", errNotFound)
	if err == nil {
		t.Fatal("expected error for ambiguous prefix")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' in error, got %v", err)
	}
}

func TestResolveRef_NotFound(t *testing.T) {
	items := map[string]*int{"abc": ptr(1)}
	names := map[string]string{}

	_, err := ResolveRef(items, names, "zzz", errNotFound)
	if !errors.Is(err, errNotFound) {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestResolveRef_ShortPrefix(t *testing.T) {
	items := map[string]*int{"abc": ptr(1)}
	names := map[string]string{}

	// Less than 3 chars should not match prefix.
	_, err := ResolveRef(items, names, "ab", errNotFound)
	if !errors.Is(err, errNotFound) {
		t.Errorf("expected errNotFound for short prefix, got %v", err)
	}
}

func TestResolveRef_NamePointsToNil(t *testing.T) {
	items := map[string]*int{"abc": nil}
	names := map[string]string{"myvm": "abc"}

	_, err := ResolveRef(items, names, "myvm", errNotFound)
	if !errors.Is(err, errNotFound) {
		t.Errorf("expected errNotFound for nil item, got %v", err)
	}
}

func TestResolveRefs_Basic(t *testing.T) {
	items := map[string]*int{"aaa": ptr(1), "bbb": ptr(2)}
	names := map[string]string{"vm1": "aaa", "vm2": "bbb"}

	ids, err := ResolveRefs(items, names, []string{"vm1", "vm2"}, errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d ids, want 2", len(ids))
	}
}

func TestResolveRefs_Dedup(t *testing.T) {
	items := map[string]*int{"aaa": ptr(1)}
	names := map[string]string{"vm1": "aaa"}

	ids, err := ResolveRefs(items, names, []string{"aaa", "vm1", "aaa"}, errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Errorf("got %d, want 1 (deduplicated)", len(ids))
	}
}

func TestResolveRefs_Error(t *testing.T) {
	items := map[string]*int{"aaa": ptr(1)}
	names := map[string]string{}

	_, err := ResolveRefs(items, names, []string{"aaa", "missing"}, errNotFound)
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
}

func TestResolveRefs_Empty(t *testing.T) {
	items := map[string]*int{}
	names := map[string]string{}

	ids, err := ResolveRefs(items, names, nil, errNotFound)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestInitNamedIndex_NilMaps(t *testing.T) {
	var items map[string]*int
	var names map[string]string

	InitNamedIndex(&items, &names)

	if items == nil {
		t.Error("items should be initialized")
	}
	if names == nil {
		t.Error("names should be initialized")
	}
}

func TestInitNamedIndex_AlreadyInitialized(t *testing.T) {
	items := map[string]*int{"a": ptr(1)}
	names := map[string]string{"n": "a"}

	InitNamedIndex(&items, &names)

	// Should not reset existing data.
	if len(items) != 1 || items["a"] == nil {
		t.Error("items was reset")
	}
	if len(names) != 1 || names["n"] != "a" {
		t.Error("names was reset")
	}
}

func ptr[T any](v T) *T { return &v }
