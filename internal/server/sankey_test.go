package server

import (
	"strconv"
	"strings"
	"testing"
)

func TestBuildSankey(t *testing.T) {
	expenses := []budgetExpense{
		{Title: "Hotel", Category: "Lodging", Amount: 300, PayerID: "alice", PayerName: "Alice", PayerColor: "#2563eb"},
		{Title: "Dinner", Category: "Food", Amount: 100, PayerID: "alice", PayerName: "Alice", PayerColor: "#2563eb"},
		{Title: "Tickets", Category: "Food", Amount: 60, PayerID: "bob", PayerName: "Bob", PayerColor: "#db2777"},
		{Title: "Taxi", Category: "", Amount: 40}, // no payer, no category
	}
	cats := []budgetCategory{
		{Name: "Lodging", Amount: 300},
		{Name: "Food", Amount: 160},
		{Name: "", Amount: 40},
	}

	sk := buildSankey(expenses, cats, "€", "Unassigned", "Uncategorized")
	if sk == nil {
		t.Fatal("buildSankey returned nil for a non-empty budget")
	}
	// Two payers plus the unassigned bucket, three categories.
	if want := 3 + 3; len(sk.Nodes) != want {
		t.Fatalf("nodes = %d, want %d", len(sk.Nodes), want)
	}
	// alice->Lodging, alice->Food, bob->Food, unassigned->uncategorized.
	if len(sk.Links) != 4 {
		t.Fatalf("links = %d, want 4: %+v", len(sk.Links), sk.Links)
	}
	if sk.Width != sankeyWidth || sk.Height <= 0 {
		t.Fatalf("unexpected canvas: %dx%d", sk.Width, sk.Height)
	}

	// The payer column is ordered by amount paid, with the unassigned bucket last.
	gotOrder := []string{sk.Nodes[0].Label, sk.Nodes[1].Label, sk.Nodes[2].Label}
	want := []string{"Alice", "Bob", "Unassigned"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("payer order = %v, want %v", gotOrder, want)
		}
	}
	// The empty category name falls back to the localized label.
	if sk.Nodes[5].Label != "Uncategorized" {
		t.Fatalf("uncategorized label = %q", sk.Nodes[5].Label)
	}
	// Category bars carry a palette class, payer bars a person color.
	if sk.Nodes[0].CatClass != "" || sk.Nodes[0].Color != "#2563eb" {
		t.Fatalf("payer node: %+v", sk.Nodes[0])
	}
	if sk.Nodes[3].CatClass == "" {
		t.Fatalf("category node without palette class: %+v", sk.Nodes[3])
	}

	// Both columns carry the same total height, so the ribbons balance out.
	var leftH, rightH float64
	for i, n := range sk.Nodes {
		h := parseSVGNum(t, n.H)
		if h <= 0 {
			t.Fatalf("node %d has no height: %+v", i, n)
		}
		if i < 3 {
			leftH += h
		} else {
			rightH += h
		}
	}
	if diff := leftH - rightH; diff > 0.5 || diff < -0.5 {
		t.Fatalf("column heights differ: left=%.2f right=%.2f", leftH, rightH)
	}

	for _, l := range sk.Links {
		if !strings.HasPrefix(l.D, "M") || !strings.HasSuffix(l.D, "Z") {
			t.Fatalf("malformed ribbon path: %q", l.D)
		}
	}
}

func TestBuildSankeyEmpty(t *testing.T) {
	if sk := buildSankey(nil, nil, "€", "Unassigned", "Uncategorized"); sk != nil {
		t.Fatalf("expected nil for an empty budget, got %+v", sk)
	}
	// Categories without any positive expense must not produce a diagram either.
	cats := []budgetCategory{{Name: "Food", Amount: 0}}
	if sk := buildSankey(nil, cats, "€", "Unassigned", "Uncategorized"); sk != nil {
		t.Fatalf("expected nil without expenses, got %+v", sk)
	}
}

func parseSVGNum(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return f
}
