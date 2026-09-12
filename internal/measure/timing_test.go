package measure

import (
	"testing"
	"time"
)

func TestMilliseconds(t *testing.T) {
	if Milliseconds(0) != nil {
		t.Fatal("unresolved timing must be null")
	}
	if value := Milliseconds(1250 * time.Microsecond); value == nil || *value != 1.25 {
		t.Fatal("duration conversion failed")
	}
}
