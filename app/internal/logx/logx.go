package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Rec struct {
	TS  int64  `json:"ts"`
	Lvl string `json:"lvl"`
	Tag string `json:"tag,omitempty"`
	Msg string `json:"msg"`
}

type core struct {
	mu    sync.Mutex
	min   int
	ring  []Rec
	sink  *os.File
	subs  []func(Rec)
	quiet bool
}

type Log struct {
	prefix string
	c      *core
	tag    string
}

var levels = map[string]int{"debug": 10, "info": 20, "warn": 30, "error": 40}

func New(level string) *Log {
	min, ok := levels[level]
	if !ok {
		min = 20
	}
	return &Log{c: &core{min: min}}
}

func (l *Log) Child(tag string) *Log { return &Log{c: l.c, tag: l.tag + tag, prefix: l.prefix} }

func (l *Log) SetFile(f *os.File) {
	l.c.mu.Lock()
	l.c.sink = f
	l.c.mu.Unlock()
}

func (l *Log) SetLevel(level string) {
	if min, ok := levels[level]; ok {
		l.c.mu.Lock()
		l.c.min = min
		l.c.mu.Unlock()
	}
}

func (l *Log) SetQuiet(q bool) {
	l.c.mu.Lock()
	l.c.quiet = q
	l.c.mu.Unlock()
}

func (l *Log) Subscribe(f func(Rec)) func() {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	l.c.subs = append(l.c.subs, f)
	i := len(l.c.subs) - 1
	return func() {
		l.c.mu.Lock()
		l.c.subs[i] = func(Rec) {}
		l.c.mu.Unlock()
	}
}

func (l *Log) at(lvl string, msg string) {
	l.c.mu.Lock()
	if levels[lvl] < l.c.min {
		l.c.mu.Unlock()
		return
	}
	r := Rec{TS: time.Now().UnixMilli(), Lvl: lvl, Tag: l.prefix + l.tag, Msg: strings.TrimSpace(msg)}
	l.c.ring = append(l.c.ring, r)
	if len(l.c.ring) > 800 {
		l.c.ring = l.c.ring[len(l.c.ring)-800:]
	}
	subs := append([]func(Rec){}, l.c.subs...)
	sink, quiet := l.c.sink, l.c.quiet
	l.c.mu.Unlock()
	if sink != nil {
		fmt.Fprintf(sink, "%s %-5s %s %s\n", time.Now().Format("15:04:05"), strings.ToUpper(lvl), r.Tag, r.Msg)
	}
	if !quiet {
		fmt.Printf("%s %-5s %s%s %s\x1b[0m\n", time.Now().Format("15:04:05"), strings.ToUpper(lvl), "\x1b[90m"+r.Tag+" ", r.Msg, "")
	}
	for _, s := range subs {
		s(r)
	}
}

func (l *Log) Debug(f string, a ...any) { l.at("debug", fmt.Sprintf(f, a...)) }
func (l *Log) Info(f string, a ...any)  { l.at("info", fmt.Sprintf(f, a...)) }
func (l *Log) Warn(f string, a ...any)  { l.at("warn", fmt.Sprintf(f, a...)) }
func (l *Log) Error(f string, a ...any) { l.at("error", fmt.Sprintf(f, a...)) }

func (l *Log) Recent(n int) []Rec {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	if len(l.c.ring) <= n {
		return append([]Rec{}, l.c.ring...)
	}
	return append([]Rec{}, l.c.ring[len(l.c.ring)-n:]...)
}

func (l *Log) SetPrefix(p string) {
	l.c.mu.Lock()
	l.prefix = strings.TrimSpace(p)
	l.c.mu.Unlock()
}
