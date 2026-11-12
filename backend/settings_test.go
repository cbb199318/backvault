package main

import (
	"testing"
	"time"
)

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	cases := []struct {
		h, m     int
		from, to string
		want     bool
	}{
		{9, 0, "08:00", "23:00", true}, {7, 59, "08:00", "23:00", false}, {23, 0, "08:00", "23:00", false},
		{23, 30, "22:00", "06:00", true}, {5, 0, "22:00", "06:00", true}, {12, 0, "22:00", "06:00", false},
	}
	for _, c := range cases {
		if got := inWindow(at(c.h, c.m), c.from, c.to); got != c.want {
			t.Errorf("%02d:%02d in %s-%s = %v, want %v", c.h, c.m, c.from, c.to, got, c.want)
		}
	}
}

func TestRemoteOfURI(t *testing.T) {
	for in, want := range map[string]string{
		"rclone:ol:115/backup": "ol", "rclone:wd-1a2b3c4d:nas/video": "wd-1a2b3c4d", "ol:tianyi/x": "ol", "/mnt/repo": "", "s3:host/b": "",
	} {
		if got := remoteOfURI(in); got != want {
			t.Errorf("remoteOfURI(%q) = %q, want %q", in, got, want)
		}
	}
}
