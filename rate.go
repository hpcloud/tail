package tail

// RateMonitor is a naive rate monitor that monitors the number of
// items processed in the current second.
type RateMonitor struct {
	second int64
	num    int64
}

func (r *RateMonitor) Tick(unixTime int64) int64 {
	if r.second != unixTime {
		r.second = unixTime
		r.num = 1
	} else {
		r.num += 1
	}
	return r.num
}
