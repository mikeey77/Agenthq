package main

import (
	"testing"
	"time"
)

func TestScheduleNext(t *testing.T) {
	mon := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local) // Monday 10:00
	d := &Schedule{Kind: "daily", Time: "08:30", Mission: "x"}
	if n := d.next(mon); n.Day() != 24 || n.Hour() != 8 || n.Minute() != 30 {
		t.Fatal("daily", n)
	}
	w := &Schedule{Kind: "weekly", Time: "09:00", Days: []int{5}, Mission: "x"} // Friday
	if n := w.next(mon); n.Weekday() != time.Friday || n.Day() != 27 {
		t.Fatal("weekly", n)
	}
	i := &Schedule{Kind: "interval", Every: 120, Mission: "x", LastRun: mon}
	if n := i.next(mon.Add(time.Minute)); !n.Equal(mon.Add(2 * time.Hour)) {
		t.Fatal("interval", n)
	}
	o := &Schedule{Kind: "once", Time: "07:00", Date: "2026-11-25", Mission: "x"}
	if n := o.next(mon); n.Day() != 25 || n.Hour() != 7 {
		t.Fatal("once", n)
	}
	o.Runs = 1
	if !o.next(mon).IsZero() {
		t.Fatal("once ran")
	}
	bad := &Schedule{Kind: "weekly", Time: "25:00", Mission: "x"}
	if bad.validate() == nil {
		t.Fatal("validate")
	}
}
