package loop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A page that floods setTimeout hits MaxTimers (256): the excess throws,
// the page can catch it, and the loop still runs the timers it accepted.
func TestTimerFloodIsCappedAndLoopStaysResponsive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 4096)
	l := New(ctx, Config{Width: 200, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<body><p id="p">0</p><script>
	  setTimeout(() => { document.getElementById("p").textContent = "alive"; }, 1); // registered before the flood: once saturated, nothing else gets a slot
	  let made = 0, refused = 0;
	  for (let i = 0; i < 2000; i++) { try { setTimeout(() => {}, 0); made++; } catch (e) { refused++; } } // the cap throws "too many timers"
	  console.log("timers " + made + " " + refused);
	</script></body>`, "https://example.test/", nil)
	sawCap, sawAlive := false, false
	deadline := time.After(5 * time.Second)
	for !(sawCap && sawAlive) {
		select {
		case <-deadline:
			t.Fatalf("cap=%v alive=%v", sawCap, sawAlive)
		case m := <-sink:
			switch v := m.(type) {
			case LogMsg:
				if strings.HasPrefix(v.Line, "timers ") {
					var made, refused int
					fmt.Sscanf(v.Line, "timers %d %d", &made, &refused)
					sawCap = made <= 255 && refused > 0 && made+refused == 2000 // 255: the alive timer holds a slot
				}
			case FrameMsg:
				if v.Err == nil {
					for _, b := range v.Result.Boxes {
						if b.Text == "alive" {
							sawAlive = true
						}
					}
				}
			}
		}
	}
}
