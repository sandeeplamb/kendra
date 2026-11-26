package kube

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a parsed 5-field cron schedule (minute hour day-of-month month day-of-week).
type cronSpec struct {
	min, hour, dom, month, dow uint64
	domStar, dowStar           bool
}

var cronMacros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

func cronNumber(s string, names map[string]int) (int, error) {
	if n, ok := names[strings.ToLower(s)]; ok {
		return n, nil
	}
	return strconv.Atoi(s)
}

// cronField turns "*/15", "1-5", "mon,wed" ... into a bit set over [lo, hi].
func cronField(f string, lo, hi int, names map[string]int) (set uint64, star bool, err error) {
	for _, part := range strings.Split(f, ",") {
		rng, step := part, 1
		if i := strings.Index(part, "/"); i >= 0 {
			rng = part[:i]
			if step, err = strconv.Atoi(part[i+1:]); err != nil || step < 1 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
		}
		a, b := lo, hi
		switch {
		case rng == "*" || rng == "?":
			star = star || step == 1
		case strings.Contains(rng, "-"):
			p := strings.SplitN(rng, "-", 2)
			var e1, e2 error
			a, e1 = cronNumber(p[0], names)
			b, e2 = cronNumber(p[1], names)
			if e1 != nil || e2 != nil {
				return 0, false, fmt.Errorf("bad range %q", part)
			}
		default:
			n, e := cronNumber(rng, names)
			if e != nil {
				return 0, false, fmt.Errorf("bad value %q", part)
			}
			a, b = n, n
			if strings.Contains(part, "/") {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return 0, false, fmt.Errorf("%q is out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, star, nil
}

func parseCron(spec string) (*cronSpec, error) {
	spec = strings.TrimSpace(spec)
	for _, p := range []string{"CRON_TZ=", "TZ="} {
		if strings.HasPrefix(spec, p) {
			if i := strings.Index(spec, " "); i > 0 {
				spec = strings.TrimSpace(spec[i:])
			}
		}
	}
	if m, ok := cronMacros[strings.ToLower(spec)]; ok {
		spec = m
	}
	f := strings.Fields(spec)
	if len(f) != 5 {
		return nil, fmt.Errorf("schedule %q needs 5 fields", spec)
	}
	var c cronSpec
	var err error
	if c.min, _, err = cronField(f[0], 0, 59, nil); err != nil {
		return nil, err
	}
	if c.hour, _, err = cronField(f[1], 0, 23, nil); err != nil {
		return nil, err
	}
	if c.dom, c.domStar, err = cronField(f[2], 1, 31, nil); err != nil {
		return nil, err
	}
	if c.month, _, err = cronField(f[3], 1, 12, monthNames); err != nil {
		return nil, err
	}
	if c.dow, c.dowStar, err = cronField(f[4], 0, 7, dayNames); err != nil {
		return nil, err
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow |= 1
	}
	return &c, nil
}

func (c *cronSpec) dayOK(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar || c.dowStar {
		return dom && dow
	}
	return dom || dow
}

// next returns the first run time strictly after t, in t's location. The zero time means none
// within five years (for example 31 February).
func (c *cronSpec) next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !c.dayOK(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case c.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case c.min&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
