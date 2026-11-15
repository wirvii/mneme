package risk

import (
	"errors"
	"reflect"
	"testing"
)

func TestDefaultTable(t *testing.T) {
	tbl := DefaultTable()
	want := map[Item][]string{
		ItemIrreversibleData: {"**/*.sql", "**/migrations/**", "**/schema.*"},
		ItemSecurity: {
			"**/.env", "**/.env.*", "**/*secret*", "**/*secret*/**",
			"**/*credential*", "**/*credential*/**", "**/*.pem", "**/*.key", "**/*.p12",
		},
		ItemDelivery: {
			"cmd/**", "internal/install/assets/**", ".github/workflows/**", "**/deploy/**",
			"**/Dockerfile*", "**/release*", "**/.goreleaser*", "**/go.mod", "**/go.sum",
			"**/package.json", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock",
			"**/bun.lockb", "**/Cargo.lock", "**/poetry.lock", "**/uv.lock",
			"**/Gemfile.lock", "**/composer.lock",
		},
	}
	for item, globs := range want {
		if !reflect.DeepEqual(tbl[item], globs) {
			t.Errorf("item %d = %v, want %v", item, tbl[item], globs)
		}
	}
	for _, item := range []Item{ItemContracts, ItemConcurrency, ItemUntestable} {
		if _, ok := tbl[item]; ok {
			t.Errorf("item %d present in DefaultTable, want absent", item)
		}
	}
	for _, g := range []string{"**/deploy/**", "**/package.json"} {
		if !contains(tbl[ItemDelivery], g) {
			t.Errorf("item 5 lacks %q", g)
		}
	}

	tbl[ItemIrreversibleData][0] = "mutated"
	tbl[ItemSecurity] = nil
	again := DefaultTable()
	if again[ItemIrreversibleData][0] != "**/*.sql" || len(again[ItemSecurity]) == 0 {
		t.Error("DefaultTable returned shared state: mutating one copy changed another")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestItems(t *testing.T) {
	got := Items()
	want := []Item{1, 2, 3, 4, 5, 6}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Items() = %v, want %v", got, want)
	}
	got[0] = 99
	if Items()[0] != 1 {
		t.Error("Items() returned shared state")
	}
}

func TestItemTitle(t *testing.T) {
	tests := []struct {
		item Item
		want string
	}{
		{1, "Irreversible data or migrations"},
		{2, "Security and permissions"},
		{3, "Contracts others consume"},
		{4, "Concurrency"},
		{5, "Delivery or environment"},
		{6, "No test would catch the regression"},
		{0, ""},
		{7, ""},
	}
	for _, tt := range tests {
		if got := tt.item.Title(); got != tt.want {
			t.Errorf("Item(%d).Title() = %q, want %q", tt.item, got, tt.want)
		}
	}
}

func TestItemValid(t *testing.T) {
	for i := Item(-1); i <= 8; i++ {
		want := i >= 1 && i <= 6
		if got := i.Valid(); got != want {
			t.Errorf("Item(%d).Valid() = %v, want %v", i, got, want)
		}
	}
}

func TestItemPathDetectable(t *testing.T) {
	for i := Item(0); i <= 7; i++ {
		want := i == 1 || i == 2 || i == 5
		if got := i.PathDetectable(); got != want {
			t.Errorf("Item(%d).PathDetectable() = %v, want %v", i, got, want)
		}
	}
}

func TestTableWith(t *testing.T) {
	base := Table{1: {"a", "b"}, 5: {"c"}}

	t.Run("adds_without_duplicates_or_removal", func(t *testing.T) {
		got, err := base.With(Table{1: {"b", "x"}, 2: {"y"}, 5: {"c", "z"}})
		if err != nil {
			t.Fatal(err)
		}
		want := Table{1: {"a", "b", "x"}, 2: {"y"}, 5: {"c", "z"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("With = %v, want %v", got, want)
		}
		if !reflect.DeepEqual(base, Table{1: {"a", "b"}, 5: {"c"}}) {
			t.Error("With mutated its receiver")
		}
	})

	t.Run("nil_extra_is_copy", func(t *testing.T) {
		got, err := base.With(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, base) {
			t.Errorf("With(nil) = %v, want %v", got, base)
		}
		got[1][0] = "mutated"
		if base[1][0] != "a" {
			t.Error("With(nil) shares backing arrays with the receiver")
		}
	})

	for _, item := range []Item{3, 4, 6, 0, 7} {
		if _, err := base.With(Table{item: {"x"}}); !errors.Is(err, ErrItemNotPathDetectable) {
			t.Errorf("With(item %d) err = %v, want ErrItemNotPathDetectable", item, err)
		}
	}
	for _, g := range []string{"", "   "} {
		if _, err := base.With(Table{2: {g}}); !errors.Is(err, ErrEmptyGlob) {
			t.Errorf("With(%q) err = %v, want ErrEmptyGlob", g, err)
		}
	}

	t.Run("globs_order", func(t *testing.T) {
		tbl := Table{5: {"e"}, 1: {"a"}, 2: {"s"}}
		want := []string{"a", "s", "e"}
		for i := 0; i < 3; i++ {
			if got := tbl.Globs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Globs() = %v, want %v", got, want)
			}
		}
	})
}
