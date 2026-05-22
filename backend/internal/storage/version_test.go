package storage

import (
	"testing"
	"time"
)

func TestVersionCompare_SeqWins(t *testing.T) {
	now := time.Now()
	a := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-a"}
	b := Version{Seq: 3, UpdatedAt: now.Add(time.Hour), UpdatedBy: "node-z"}
	if a.Compare(b) != 1 {
		t.Fatalf("expected a > b (higher Seq wins regardless of newer UpdatedAt or higher node name)")
	}
	if b.Compare(a) != -1 {
		t.Fatalf("expected b < a")
	}
}

func TestVersionCompare_TimestampTiebreaker(t *testing.T) {
	now := time.Now()
	a := Version{Seq: 5, UpdatedAt: now.Add(time.Second), UpdatedBy: "node-a"}
	b := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-z"}
	if a.Compare(b) != 1 {
		t.Fatalf("expected a > b (newer UpdatedAt wins when Seq equal)")
	}
}

func TestVersionCompare_NodeNameTiebreaker(t *testing.T) {
	now := time.Now()
	a := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-z"}
	b := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-a"}
	if a.Compare(b) != 1 {
		t.Fatalf("expected a > b (lex greater UpdatedBy wins)")
	}
	if b.Compare(a) != -1 {
		t.Fatalf("expected b < a")
	}
}

func TestVersionCompare_Equal(t *testing.T) {
	now := time.Now()
	a := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-a"}
	b := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-a"}
	if a.Compare(b) != 0 {
		t.Fatalf("expected equal")
	}
}

func TestVersionCompare_Deterministic(t *testing.T) {
	// Same comparison must always return same result regardless of order
	now := time.Now()
	a := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-b"}
	b := Version{Seq: 5, UpdatedAt: now, UpdatedBy: "node-a"}
	for i := 0; i < 100; i++ {
		if a.Compare(b) != 1 {
			t.Fatalf("non-deterministic: a should always win")
		}
		if b.Compare(a) != -1 {
			t.Fatalf("non-deterministic: b should always lose")
		}
	}
}

func TestVersionIsZero(t *testing.T) {
	if !(Version{}).IsZero() {
		t.Fatal("zero value should be IsZero")
	}
	if (Version{Seq: 1}).IsZero() {
		t.Fatal("Seq set should not be IsZero")
	}
	if (Version{UpdatedBy: "a"}).IsZero() {
		t.Fatal("UpdatedBy set should not be IsZero")
	}
}

func TestNextVersion_FromZero(t *testing.T) {
	now := time.Now()
	v := NextVersion(Version{}, "node-1", now)
	if v.Seq != 1 {
		t.Fatalf("expected Seq=1 from zero, got %d", v.Seq)
	}
	if v.UpdatedBy != "node-1" {
		t.Fatalf("unexpected UpdatedBy: %q", v.UpdatedBy)
	}
	if !v.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt mismatch")
	}
}

func TestNextVersion_Increments(t *testing.T) {
	now := time.Now()
	prev := Version{Seq: 42, UpdatedAt: now.Add(-time.Hour), UpdatedBy: "node-old"}
	v := NextVersion(prev, "node-new", now)
	if v.Seq != 43 {
		t.Fatalf("expected Seq=43, got %d", v.Seq)
	}
	if v.UpdatedBy != "node-new" {
		t.Fatalf("UpdatedBy should be new node")
	}
	// NextVersion-derived must beat prev
	if v.Compare(prev) != 1 {
		t.Fatal("new version must beat prev")
	}
}
