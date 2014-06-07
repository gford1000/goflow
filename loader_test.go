package flow

import (
	"sync"
	"testing"
)

// Starter component fires the network
// by sending a number given in its constructor
// to its output port.
type starter struct {
	Component
	Start <-chan float64
	Out   chan<- int
}

func (s *starter) OnStart(num float64) {
	s.Out <- int(num)
}

func newStarter() interface{} {
	s := new(starter)
	return s
}

func init() {
	Register("starter", newStarter)
}

// SequenceGenerator generates a sequence of integers
// from 0 to a number passed to its input.
type sequenceGenerator struct {
	Component
	Num      <-chan int
	Sequence chan<- int
}

func (s *sequenceGenerator) OnNum(n int) {
	for i := 1; i <= n; i++ {
		s.Sequence <- i
	}
}

func newSequenceGenerator() interface{} {
	return new(sequenceGenerator)
}

func init() {
	Register("sequenceGenerator", newSequenceGenerator)
}

// Summarizer component sums all its input packets and
// produces a sum output just before shutdown
// Utilizes Initializable and Lockable interfaces to
// provide concurrent access to state, correctly initialised
type summarizer struct {
	Component
	In        <-chan int
	Sum       chan<- int
	stateLock *sync.Mutex

	current int
}

func (s *summarizer) Init() {
	s.stateLock = new(sync.Mutex)
}

func (s *summarizer) Lock() {
	s.stateLock.Lock()
}

func (s *summarizer) Unlock() {
	s.stateLock.Unlock()
}

func newSummarizer() interface{} {
	s := new(summarizer)
	s.Component.setMode(ComponentModeSync)
	return s
}

func init() {
	Register("summarizer", newSummarizer)
}

func (s *summarizer) OnIn(i int) {
	s.current += i
}

func (s *summarizer) Finish() {
	s.Sum <- s.current
}

var runtimeNetworkJSON = `{
	"properties": {
		"name": "runtimeNetwork"
	},
	"processes": {
		"starter": {
			"component": "starter"
		},
		"generator": {
			"component": "sequenceGenerator"
		},
		"doubler": {
			"component": "doubler"
		},
		"sum": {
			"component": "summarizer"
		}
	},
	"connections": [
		{
			"data": 10,
			"tgt": {
				"process": "starter",
				"port": "Start"
			}
		},
		{
			"src": {
				"process": "starter",
				"port": "Out"
			},
			"tgt": {
				"process": "generator",
				"port": "Num"
			}
		},
		{
			"src": {
				"process": "generator",
				"port": "Sequence"
			},
			"tgt": {
				"process": "doubler",
				"port": "In"
			}
		},
		{
			"src": {
				"process": "doubler",
				"port": "Out"
			},
			"tgt": {
				"process": "sum",
				"port": "In"
			}
		}
	],
	"exports": [
		{
			"private": "starter.Start",
			"public": "Start"
		},
		{
			"private": "sum.Sum",
			"public": "Out"
		}
	]
}`

func TestRuntimeNetwork(t *testing.T) {
	net := ParseJSON([]byte(runtimeNetworkJSON))
	if net == nil {
		t.Error("Could not load JSON")
	}

	start := make(chan float64)
	out := make(chan int)

	net.SetInPort("Start", start)
	net.SetOutPort("Out", out)

	runNet(net)

	// Wait for the network setup
	<-net.SuspendUntilCanAcceptInputs()

	// Close start to halt it normally
	close(start)

	i := <-out
	if i != 110 {
		t.Errorf("Wrong result: %d != 110", i)
	}

	<-net.SuspendUntilFinished()
}
