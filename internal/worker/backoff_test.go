package worker

import (
	"testing"
	"time"
)

func TestCommentBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{
		1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 7: 32 * time.Minute, 8: time.Hour, 50: time.Hour,
	} {
		if got := commentBackoff(attempts); got != want {
			t.Errorf("commentBackoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
