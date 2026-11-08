//go:build !windows && !darwin

package tray

var stop = make(chan struct{})

// Run — без значка: ждём Stop.
func Run(m Menu, ready func()) {
	_ = label(m)
	if ready != nil {
		ready()
	}
	<-stop
}

func Stop() {
	select {
	case <-stop:
	default:
		close(stop)
	}
}
