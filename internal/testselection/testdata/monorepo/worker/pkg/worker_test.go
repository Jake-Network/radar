package worker

import "testing"

func TestPrice(t *testing.T) {
	if Price() != 100 {
		t.Fatal("price")
	}
}
