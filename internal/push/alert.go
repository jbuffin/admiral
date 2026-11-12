package push

// WatchedWindows is the part of the server's hub the alert sink needs.
type WatchedWindows interface{ SendWatching(v any) }

// AlertSink sends the Alert outcome to the windows being watched; a Push outcome is not its business.
type AlertSink struct{ windows WatchedWindows }

func NewAlertSink(w WatchedWindows) *AlertSink { return &AlertSink{windows: w} }

type alertMessage struct {
	Type string `json:"type"`
	Payload
}

func (a *AlertSink) Deliver(d Decided) {
	if d.Outcome != Alert {
		return
	}
	a.windows.SendWatching(alertMessage{Type: "alert", Payload: PayloadOf(d.Event)})
}

// Async queues sends and writes them from one goroutine in order, so a slow window never holds up the feed.
// A full queue drops the alert: it is only a chime and a toast.
func Async(w WatchedWindows) WatchedWindows {
	a := &asyncWindows{q: make(chan any, 64)}
	go func() {
		for v := range a.q {
			w.SendWatching(v)
		}
	}()
	return a
}

type asyncWindows struct{ q chan any }

func (a *asyncWindows) SendWatching(v any) {
	select {
	case a.q <- v:
	default:
	}
}
