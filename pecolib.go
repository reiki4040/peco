package peco

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/nsf/termbox-go"
)

type PecoOptions struct {
	OptTTY            string `long:"tty" description:"path to the TTY (usually, the value of $TTY)"`
	OptQuery          string `long:"query" description:"initial value for query"`
	OptRcfile         string `long:"rcfile" description:"path to the settings file"`
	OptNoIgnoreCase   bool   `long:"no-ignore-case" description:"start in case-sensitive-mode (DEPRECATED)" default:"false"`
	OptBufferSize     int    `long:"buffer-size" short:"b" description:"number of lines to keep in search buffer"`
	OptEnableNullSep  bool   `long:"null" description:"expect NUL (\\0) as separator for target/output"`
	OptInitialIndex   int    `long:"initial-index" description:"position of the initial index of the selection (0 base)"`
	OptInitialMatcher string `long:"initial-matcher" description:"specify the default matcher"`
	OptPrompt         string `long:"prompt" description:"specify the prompt string"`
	OptLayout         string `long:"layout" description:"layout to be used 'top-down' (default) or 'bottom-up'"`
}

func NewPecoOption() *PecoOptions {
	return &PecoOptions{}
}

// BufferSize returns the specified buffer size. Fulfills CtxOptions
func (o PecoOptions) BufferSize() int {
	return o.OptBufferSize
}

// EnableNullSep returns tru if --null was specified. Fulfills CtxOptions
func (o PecoOptions) EnableNullSep() bool {
	return o.OptEnableNullSep
}

func (o PecoOptions) InitialIndex() int {
	if o.OptInitialIndex >= 0 {
		return o.OptInitialIndex + 1
	}
	return 1
}

func (o PecoOptions) LayoutType() string {
	return o.OptLayout
}

type ChoicesHelper struct {
	*Ctx
}

func (i *ChoicesHelper) draw(choices []Match) {
	m := &sync.Mutex{}
	var refresh *time.Timer

	i.lines = choices
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

type Choosable interface {
	Choice() string
}

func Pecolib(choices []Choosable) ([]interface{}, error) {
	return pecolibWrap(choices, &PecoOptions{})
}

func PecolibWithPrompt(choices []Choosable, prompt string) ([]interface{}, error) {
	return pecolibWrap(choices, &PecoOptions{OptPrompt: prompt})
}

func PecolibWithOptions(choices []Choosable, opts *PecoOptions) ([]interface{}, error) {
	return pecolibWrap(choices, opts)
}

func pecolibWrap(choices []Choosable, opts *PecoOptions) ([]interface{}, error) {
	if choices == nil {
		return nil, errors.New("choices is nil.")
	}
	if len(choices) == 0 {
		return nil, errors.New("choices is empty.")
	}

	choiceMap := make(map[string]interface{})
	matches := make([]Match, 0, len(choices))
	for _, c := range choices {
		if c == nil {
			continue
		}
		s := c.Choice()
		choiceMap[s] = c

		// TODO investigate NewNoMatch boolean
		matches = append(matches, NewNoMatch(s, true))
	}

	if len(matches) == 0 {
		return nil, errors.New("choices are all nil.")
	}

	matched, err := pecolib(matches, opts)
	if err != nil {
		return nil, err
	}

	ret := make([]interface{}, 0, len(matched))
	for _, m := range matched {
		s := m.Output()
		if v, ok := choiceMap[s]; ok {
			ret = append(ret, v)
		} else {
			return nil, errors.New("internal error")
		}
	}

	return ret, nil
}

func pecolib(choices []Match, opts *PecoOptions) ([]Match, error) {
	var err error
	var out []Match

	if envvar := os.Getenv("GOMAXPROCS"); envvar == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
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

	if len(opts.OptPrompt) > 0 {
		ctx.SetPrompt(opts.OptPrompt)
	}

	if len(opts.OptInitialMatcher) > 0 {
		if !ctx.SetCurrentMatcher(opts.OptInitialMatcher) {
			return nil, errors.New(fmt.Sprintf("Unknown matcher: '%s'\n", opts.OptInitialMatcher))
		}
	}

	choicesHelper := ChoicesHelper{ctx}
	choicesHelper.draw(choices)
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
