package peco

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/nsf/termbox-go"
)

var version = "v0.1.0"

type cmdOptions struct {
	OptHelp           bool   `short:"h" long:"help" description:"show this help message and exit"`
	OptTTY            string `long:"tty" description:"path to the TTY (usually, the value of $TTY)"`
	OptQuery          string `long:"query" description:"initial value for query"`
	OptRcfile         string `long:"rcfile" description:"path to the settings file"`
	OptNoIgnoreCase   bool   `long:"no-ignore-case" description:"start in case-sensitive-mode (DEPRECATED)" default:"false"`
	OptVersion        bool   `long:"version" description:"print the version and exit"`
	OptBufferSize     int    `long:"buffer-size" short:"b" description:"number of lines to keep in search buffer"`
	OptEnableNullSep  bool   `long:"null" description:"expect NUL (\\0) as separator for target/output"`
	OptInitialIndex   int    `long:"initial-index" description:"position of the initial index of the selection (0 base)"`
	OptInitialMatcher string `long:"initial-matcher" description:"specify the default matcher"`
	OptPrompt         string `long:"prompt" description:"specify the prompt string"`
	OptLayout         string `long:"layout" description:"layout to be used 'top-down' (default) or 'bottom-up'"`
}

func showHelp() {
	// The ONLY reason we're not using go-flags' help option is
	// because I wanted to tweak the format just a bit... but
	// there wasn't an easy way to do so
	os.Stderr.WriteString(`
Usage: peco [options] [FILE]

Options:
`)

	t := reflect.TypeOf(cmdOptions{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag

		var o string
		if s := tag.Get("short"); s != "" {
			o = fmt.Sprintf("-%s, --%s", tag.Get("short"), tag.Get("long"))
		} else {
			o = fmt.Sprintf("--%s", tag.Get("long"))
		}

		fmt.Fprintf(
			os.Stderr,
			"  %-21s %s\n",
			o,
			tag.Get("description"),
		)
	}
}

// BufferSize returns the specified buffer size. Fulfills CtxOptions
func (o cmdOptions) BufferSize() int {
	return o.OptBufferSize
}

// EnableNullSep returns tru if --null was specified. Fulfills CtxOptions
func (o cmdOptions) EnableNullSep() bool {
	return o.OptEnableNullSep
}

func (o cmdOptions) InitialIndex() int {
	if o.OptInitialIndex >= 0 {
		return o.OptInitialIndex + 1
	}
	return 1
}

func (o cmdOptions) LayoutType() string {
	return o.OptLayout
}

type InObj struct {
	*Ctx
}

func (i *InObj) setSomething(in []Match) {
	m := &sync.Mutex{}
	var refresh *time.Timer

	i.lines = in
	m.Lock()
	if refresh == nil {
		refresh = time.AfterFunc(100*time.Millisecond, func() {
			if !i.ExecQuery() {
				i.DrawMatches(i.lines)
			}
			m.Lock()
			refresh = nil
			m.Unlock()
		})
	}
	m.Unlock()
}

func Pecolib(in []Match, prompt string) ([]Match, error) {
	var err error
	var out []Match

	if envvar := os.Getenv("GOMAXPROCS"); envvar == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	opts := &cmdOptions{}
	p := flags.NewParser(opts, flags.PrintErrors)
	_, err = p.Parse()
	if err != nil {
		return nil, err
	}

	if opts.OptLayout != "" {
		if !IsValidLayoutType(LayoutType(opts.OptLayout)) {
			return nil, errors.New(fmt.Sprintf("Unknown layout: '%s'\n", opts.OptLayout))
		}
	}

	ctx := NewCtx(opts)
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("Error in recover.")
			return
		}
	}()

	if opts.OptRcfile == "" {
		file, err := LocateRcfile()
		if err == nil {
			opts.OptRcfile = file
		}
	}

	// Default matcher is IgnoreCase
	ctx.SetCurrentMatcher(IgnoreCaseMatch)

	if opts.OptRcfile != "" {
		err = ctx.ReadConfig(opts.OptRcfile)
		if err != nil {
			return nil, err
		}
	}

	if len(prompt) > 0 {
		ctx.SetPrompt(prompt)
	}
	/*
		if len(opts.OptPrompt) > 0 {
			ctx.SetPrompt(opts.OptPrompt)
		}
	*/

	// Deprecated. --no-ignore-case options will be removed in later.
	if opts.OptNoIgnoreCase {
		ctx.SetCurrentMatcher(CaseSensitiveMatch)
	}

	if len(opts.OptInitialMatcher) > 0 {
		if !ctx.SetCurrentMatcher(opts.OptInitialMatcher) {
			return nil, errors.New(fmt.Sprintf("Unknown matcher: '%s'\n", opts.OptInitialMatcher))
		}
	}

	inobj := InObj{ctx}
	inobj.setSomething(in)
	err = TtyReady()
	if err != nil {
		return nil, err
	}
	defer TtyTerm()

	err = termbox.Init()
	if err != nil {
		return nil, err
	}
	defer termbox.Close()

	// Windows handle Esc/Alt self
	if runtime.GOOS == "windows" {
		termbox.SetInputMode(termbox.InputEsc | termbox.InputAlt)
	}

	view := ctx.NewView()
	filter := ctx.NewFilter()
	input := ctx.NewInput()
	sig := ctx.NewSignalHandler()

	loopers := []interface {
		Loop()
	}{
		view,
		filter,
		input,
		sig,
	}
	for _, looper := range loopers {
		ctx.AddWaitGroup(1)
		go looper.Loop()
	}

	if len(opts.OptQuery) > 0 {
		ctx.SetQuery([]rune(opts.OptQuery))
		ctx.ExecQuery()
	} else {
		view.Refresh()
	}

	ctx.WaitDone()

	st := ctx.ExitStatus()
	if st != 0 {
		return nil, errors.New(fmt.Sprintf("something error code: %d", st))
	}

	if result := ctx.Result(); result != nil {
		for _, match := range result {
			out = append(out, match)
		}
	}
	return out, err
}
