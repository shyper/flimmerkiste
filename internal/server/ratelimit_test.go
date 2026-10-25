package server

import (
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	var l rateLimit
	now := time.Now()
	for i := 0; i < rateBurst; i++ {
		if !l.allow("10.0.0.1:5000", now) {
			t.Fatalf("request %d refused within the burst", i+1)
		}
	}
	if l.allow("10.0.0.1:5001", now) {
		t.Fatal("request beyond the burst allowed (other port, same client)")
	}
	if !l.allow("10.0.0.2:5000", now) {
		t.Fatal("another client is limited too")
	}
	if !l.allow("10.0.0.1:5000", now.Add(6*time.Second)) {
		t.Fatal("no request allowed after the bucket refilled")
	}
}
