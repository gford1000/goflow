// The flow package is a framework for Flow-based programming in Go.
package flow

import (
	"reflect"
	"sync"
)

type ComponentMode int8

const (
	// ComponentModeUndefined stands for a fallback component mode (Async).
	ComponentModeUndefined ComponentMode = iota
	// ComponentModeAsync stands for asynchronous functioning mode.
	ComponentModeAsync
	// ComponentModeSync stands for synchronous functioning mode.
	ComponentModeSync
	// ComponentModePool stands for async functioning with a fixed pool.
	ComponentModePool
)

// DefaultComponentMode is the preselected functioning mode of all components being run.
var DefaultComponentMode = ComponentModeAsync

// Package-internal interface that Component conforms to - simplifies RunProc() code
type component interface {
	getIsRunning() bool
	setIsRunning(running bool)
	getNet() *Graph
	setNet(g *Graph)
	getMode() ComponentMode
	getPoolSize() uint8
	setPoolSize(size uint8)
	getTerm() chan struct{}
	closeTerm()
}

// Component is a generic flow component that has to be contained in concrete components.
// It stores network-specific information.
type Component struct {
	// Is running flag indicates that the process is currently running.
	isRunning bool
	// Net is a pointer to network to inform it when the process is started and over
	// or to change its structure at run time.
	net *Graph
	// Mode is component's functioning mode.
	mode ComponentMode
	// PoolSize is used to define pool size when using ComponentModePool.
	poolSize uint8
	// Term chan is used to terminate the process immediately without closing
	// any channels.
	term chan struct{}
}

func (c *Component) getIsRunning() bool {
	return c.isRunning
}

func (c *Component) setIsRunning(running bool) {
	c.isRunning = running
}

func (c *Component) getNet() *Graph {
	return c.net
}

func (c *Component) setNet(n *Graph) {
	c.net = n
}

func (c *Component) getMode() ComponentMode {
	return c.mode
}

func (c *Component) setMode(mode ComponentMode) {
	c.mode = mode
}

func (c *Component) getPoolSize() uint8 {
	return c.poolSize
}

func (c *Component) setPoolSize(size uint8) {
	c.poolSize = size
}

func (c *Component) getTerm() chan struct{} {
	if c.term == nil {
		c.term = make(chan struct{})
	}
	return c.term
}

func (c *Component) closeTerm() {
	term := c.term
	c.term = nil
	close(term)
}

// Initalizable is the interface implemented by components/graphs with custom initialization code.
type Initializable interface {
	Init()
}

// Finalizable is the interface implemented by components/graphs with extra finalization code.
type Finalizable interface {
	Finish()
}

// Shutdowner is the interface implemented by components overriding default Shutdown() behavior.
type Shutdowner interface {
	Shutdown()
}

type Locker interface {
	Lock()
	Unlock()
}

// postHandler is used to bind handlers to a port
type portHandler struct {
	onRecv  reflect.Value
	onClose reflect.Value
}

// RunProc runs event handling loop on component ports.
// It returns true on success or panics with error message and returns false on error.
func RunProc(c interface{}) bool {
	// Check if passed interface is a valid pointer to struct
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		panic("Argument of flow.RunProc() is not a valid pointer")
		return false
	}
	vp := v
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		panic("Argument of flow.RunProc() is not a valid pointer to structure. Got type: " + vp.Type().Name())
		return false
	}
	t := v.Type()

	// Get the embedded flow.component
	vCom, ok := c.(component)
	if !ok {
		panic("Argument of flow.RunProc() is not a flow.component")
	}

	// Get interface to internal state lock, if available
	locker, hasLock := c.(Locker)

	// Call user init function if exists
	if initable, ok := c.(Initializable); ok {
		initable.Init()
	}

	// A group to wait for all inputs to be closed
	inputsClose := new(sync.WaitGroup)
	// A group to wait for all recv handlers to finish
	handlersDone := new(sync.WaitGroup)

	// Get the component mode
	componentMode := vCom.getMode()
	poolSize := vCom.getPoolSize()

	// Create a slice of select cases and port handlers
	cases := make([]reflect.SelectCase, 0, t.NumField())
	handlers := make([]portHandler, 0, t.NumField())

	// Make and listen on termination channel
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(vCom.getTerm())})
	handlers = append(handlers, portHandler{})

	// Iterate over struct fields and bind handlers
	inputCount := 0
	for i := 0; i < t.NumField(); i++ {
		fv := v.Field(i)
		ff := t.Field(i)
		ft := fv.Type()
		// Detect control channels
		if fv.IsValid() && fv.Kind() == reflect.Chan && !fv.IsNil() && (ft.ChanDir()&reflect.RecvDir) != 0 {
			// Bind handlers for an input channel
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: fv})
			h := portHandler{onRecv: vp.MethodByName("On" + ff.Name), onClose: vp.MethodByName("On" + ff.Name + "Close")}
			handlers = append(handlers, h)
			if h.onClose.IsValid() || h.onRecv.IsValid() {
				// Add the input to the wait group
				inputsClose.Add(1)
				inputCount++
			}
		}
	}

	if inputCount == 0 {
		panic("Components with no input ports are not supported")
	}

	// Prepare handler closures
	recvHandler := func(onRecv, value reflect.Value) {
		if hasLock {
			locker.Lock()
		}
		valArr := [1]reflect.Value{value}
		onRecv.Call(valArr[:])
		if hasLock {
			locker.Unlock()
		}
		handlersDone.Done()
	}
	closeHandler := func(onClose reflect.Value) {
		if onClose.IsValid() {
			// Lock the state and call OnClose handler
			if hasLock {
				locker.Lock()
			}
			onClose.Call([]reflect.Value{})
			if hasLock {
				locker.Unlock()
			}
		}
		inputsClose.Done()
	}
	terminate := func() {
		if !vCom.getIsRunning() {
			return
		}
		vCom.setIsRunning(false)
		for i := 0; i < inputCount; i++ {
			inputsClose.Done()
		}
	}
	// closePorts closes all output channels of a process.
	closePorts := func() {
		// Iterate over struct fields
		for i := 0; i < t.NumField(); i++ {
			fv := v.Field(i)
			ft := fv.Type()
			// Detect and close send-only channels
			if fv.IsValid() {
				if fv.Kind() == reflect.Chan && (ft.ChanDir()&reflect.SendDir) != 0 && (ft.ChanDir()&reflect.RecvDir) == 0 {
					fv.Close()
				} else if fv.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Chan {
					ll := fv.Len()
					for i := 0; i < ll; i += 1 {
						fv.Index(i).Close()
					}
				}
			}
		}
	}
	// shutdown represents a standard process shutdown procedure.
	shutdown := func() {
		if s, ok := c.(Shutdowner); ok {
			// Custom shutdown behavior
			s.Shutdown()
		} else {
			// Call user finish function if exists
			if finable, ok := c.(Finalizable); ok {
				finable.Finish()
			}
			// Close all output ports if the process is still running
			if vCom.getIsRunning() {
				closePorts()
			}
		}
	}

	// Run the port handlers depending on component mode
	if componentMode == ComponentModePool && poolSize > 0 {
		// Pool mode, prefork limited goroutine pool for all inputs
		var poolIndex uint8
		poolWait := new(sync.WaitGroup)
		once := new(sync.Once)
		for poolIndex = 0; poolIndex < poolSize; poolIndex++ {
			poolWait.Add(1)
			go func() {
				for {
					chosen, recv, recvOK := reflect.Select(cases)
					if !recvOK {
						poolWait.Done()
						if chosen == 0 {
							// Term signal
							terminate()
						} else {
							// Port has been closed
							once.Do(func() {
								// Wait for other workers
								poolWait.Wait()
								// Close output down
								closeHandler(handlers[chosen].onClose)
							})
						}
						return
					}
					if handlers[chosen].onRecv.IsValid() {
						handlersDone.Add(1)
						recvHandler(handlers[chosen].onRecv, recv)
					}
				}
			}()
		}
	} else {
		go func() {
			for {
				chosen, recv, recvOK := reflect.Select(cases)
				if !recvOK {
					if chosen == 0 {
						// Term signal
						terminate()
					} else {
						// Port has been closed
						closeHandler(handlers[chosen].onClose)
					}
					return
				}
				if handlers[chosen].onRecv.IsValid() {
					handlersDone.Add(1)
					if componentMode == ComponentModeAsync || componentMode == ComponentModeUndefined && DefaultComponentMode == ComponentModeAsync {
						// Async mode
						go recvHandler(handlers[chosen].onRecv, recv)
					} else {
						// Sync mode
						recvHandler(handlers[chosen].onRecv, recv)
					}
				}
			}
		}()
	}

	// Indicate the process as running
	vCom.setIsRunning(true)

	go func() {
		// Wait for all inputs to be closed
		inputsClose.Wait()
		// Wait all inport handlers to finish their job
		handlersDone.Wait()

		// Call shutdown handler (user or default)
		shutdown()

		// Get the embedded flow.Component and check if it belongs to a network
		vNet := vCom.getNet()
		if vNet != nil {
			if vNetCtr, hasNet := reflect.ValueOf(vNet).Interface().(netController); hasNet {
				// Remove the instance from the network's WaitGroup
				vNetCtr.getWait().Done()
			}
		}
	}()
	return true
}

// StopProc terminates the process if it is running.
// It doesn't close any in or out ports of the process, so it can be
// replaced without side effects.
func StopProc(c interface{}) bool {

	// Get the embedded flow.component
	vCom, ok := c.(component)
	if !ok {
		panic("Argument of flow.StopProc() is not a flow.component")
	}

	vCom.closeTerm()
	return true
}
