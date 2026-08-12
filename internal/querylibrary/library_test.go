package querylibrary

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreFavoritesAndCustomSnippets(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "library.json")
	store := NewStore(path)
	favorite, err := store.SaveFavorite(Favorite{Name: " Active users ", SQL: "SELECT * FROM users", Tags: []string{"Ops", "ops", " active "}})
	if err != nil {
		t.Fatalf("SaveFavorite() error = %v", err)
	}
	values, err := store.Favorites("active")
	if err != nil || len(values) != 1 || values[0].ID != favorite.ID || len(values[0].Tags) != 2 {
		t.Fatalf("Favorites() = %#v, %v", values, err)
	}
	snippet, err := store.SaveSnippet(Snippet{Trigger: " mine ", Body: "SELECT ${columns} FROM ${table}"})
	if err != nil {
		t.Fatalf("SaveSnippet() error = %v", err)
	}
	snippets, err := store.Snippets("mine")
	if err != nil || len(snippets) != 1 || snippets[0].ID != snippet.ID {
		t.Fatalf("Snippets() = %#v, %v", snippets, err)
	}
	if _, err := store.SaveSnippet(Snippet{Trigger: "sel", Body: "x"}); err == nil {
		t.Fatal("SaveSnippet() allowed built-in trigger")
	}
	deleted, err := store.DeleteFavorite(favorite.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteFavorite() = %v, %v", deleted, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("permissions = %o", info.Mode().Perm())
		}
	}
}

func TestExpandReturnsNavigablePlaceholders(t *testing.T) {
	t.Parallel()
	value := "-- query\nsel"
	expanded, placeholders, ok := Expand(value, len(value), BuiltInSnippets())
	if !ok || expanded != "-- query\nSELECT\n    ${columns}\nFROM ${table};" {
		t.Fatalf("Expand() = %q, %#v, %v", expanded, placeholders, ok)
	}
	if len(placeholders) != 2 || expanded[placeholders[0].Start:placeholders[0].End] != "${columns}" || expanded[placeholders[1].Start:placeholders[1].End] != "${table}" {
		t.Fatalf("placeholders = %#v", placeholders)
	}
}

func TestFindPlaceholdersReturnsEveryOccurrence(t *testing.T) {
	t.Parallel()

	value := "SELECT * FROM ${table} WHERE owner_id = ${id} OR reviewer_id = ${id}"
	placeholders := FindPlaceholders(value)
	if len(placeholders) != 3 {
		t.Fatalf("FindPlaceholders() returned %d placeholders, want 3", len(placeholders))
	}
	wantNames := []string{"table", "id", "id"}
	for i, placeholder := range placeholders {
		if placeholder.Name != wantNames[i] || value[placeholder.Start:placeholder.End] != "${"+wantNames[i]+"}" {
			t.Fatalf("FindPlaceholders()[%d] = %#v", i, placeholder)
		}
	}
}
