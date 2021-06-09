/*
Copyright 2016 by Milo Christiansen

This software is provided 'as-is', without any express or implied warranty. In
no event will the authors be held liable for any damages arising from the use of
this software.

Permission is granted to anyone to use this software for any purpose, including
commercial applications, and to alter it and redistribute it freely, subject to
the following restrictions:

1. The origin of this software must not be misrepresented; you must not claim
that you wrote the original software. If you use this software in a product, an
acknowledgment in the product documentation would be appreciated but is not
required.

2. Altered source versions must be plainly marked as such, and must not be
misrepresented as being the original software.

3. This notice may not be removed or altered from any source distribution.
*/

// A convenience system for managing linked groups of goroutines.
package workergroup

import "runtime"
import "errors"

// Worker is the type that that a worker function must match.
//
// If a Worker returns a non-nil error the Group Instance it belongs will be aborted and the
// error will be saved to return to the client. If multiple Workers return errors the last error
// reported to the Group Instance will be the one reported.
//
// The passed in "abort" channel will never have a value sent on it, instead it will be closed if
// an abort is ordered (either by the client or in response to an error). You should check to see
// if reads succeed on this channel regularly so you can exit early when required (returning early
// in response to an abort is NOT an error! your Worker should return nil in this case so as to not
// clobber the real error).
//
// The "data" argument allows you to optionally pass data into all the Workers in the group. This
// allows Workers to share resources such as channels without the need for the Workers to be closures.
type Worker func(abort <-chan bool, data interface{}) error

// Cleaner is the type that a cleanup function must conform to.
//
// Cleanup functions are functions that may be optionally registered to run after all the Workers
// in a Group Instance return. Cleaners will always be called in the order they were added.
// Generally you would use Cleaners to free/close resources (files, network connections, etc) stored
// in the "data" value.
//
// The "data" argument is the same value passed to the Workers.
type Cleaner func(data interface{})

// NonErrorAbort is returned by Wait if Abort is used to abort the Instance and no other errors are
// generated by the Workers.
var NonErrorAbort = errors.New("Instance aborted due to explicit order (not error triggered).")

// Group is a convenience mechanism for launching and controlling multiple goroutines.
//
// This is intended for cases where you have a set of goroutines that all work together,
// and want the ability to abort the whole set cleanly and/or launch copies of the set
// on demand. Keep in mind that that for relaunchable tasks you can get better performance
// by designing a purpose built system tuned to your specific requirements. This is simply
// a convenience with "good enough" performance, not a perfect generic solution.
//
// A Group does not store any information specific to an individual run, it is actually
// perfectly safe to modify a Group after calling Start or Run (just don't expect your
// additions to affect running Instances). In a similar vein so long as your Workers and
// Cleaners make proper use of their data values and won't clobber each other or share
// resources inappropriately you can run multiple copies of a Group in parallel.
type Group struct {
	counts   []int
	workers  []Worker
	cleaners []Cleaner
}

// Add the given Worker to the Group.
//
// When the Group launches an Instance it will contain "count" copies of the given Worker.
// If "count" is <= 0 then runtime.NumCPU copies of this worker will be launched.
func (wg *Group) Add(count int, worker Worker) {
	if count <= 0 {
		count = runtime.NumCPU()
	}

	wg.workers = append(wg.workers, worker)
	wg.counts = append(wg.counts, count)
}

// AddCleaner adds a Cleaner to the Group.
func (wg *Group) AddCleaner(clean Cleaner) {
	wg.cleaners = append(wg.cleaners, clean)
}

// I debated using "Go" rather than "Start", but decided that "Start" was clearer.

// Start launches a Group and returns the Instance tied to this particular run.
//
// "data" will be passed to the Group's Workers and Cleaners, it is perfectly fine to pass nil if
// you do not need this value.
func (wg *Group) Start(data interface{}) *Instance {
	in := &Instance{make(chan bool), make(chan bool), nil}

	rtn := make(chan error)
	w := func(i int) {
		rtn <- wg.workers[i](in.abort, data)
	}

	total := 0
	for i := range wg.workers {
		for j := 0; j < wg.counts[i]; j++ {
			total++
			go w(i)
		}
	}

	go in.run(data, wg.cleaners, total, rtn)

	return in
}

// Run launches a Group then waits for all the launched Workers to return, see Instance.Wait and Group.Start.
func (wg *Group) Run(data interface{}) error {
	// This whole system is one giant convenience method, so why not?
	return wg.Start(data).Wait()
}

// Instance is used to store state for a particular running instance of a Group.
type Instance struct {
	// Never, ever, ever send a value on either of these channels!

	// abort is closed when an abort has been ordered.
	abort chan bool

	// Closed after all workers return. Functions waiting to use err block until reads succeed.
	// There are better ways to do this, but they are more complicated.
	done chan bool

	// err hold the return value for calls to Wait for this Instance. Since no call to Wait will return before
	// done is closed, and this is set before that happens, there is no need for synchronization.
	// Never, ever, set this outside of run!
	err error
}

// run manages all aspects of waiting for workers to return, including ordering aborts and launching cleaners.
func (in *Instance) run(data interface{}, cleaners []Cleaner, total int, rtn chan error) {
	for i := 0; i < total; i++ {
		err := <-rtn
		if err != nil {
			in.err = err
			select {
			case <-in.abort:
			default:
				close(in.abort)
			}
		}
	}

	for _, c := range cleaners {
		c(data)
	}

	// Make sure that there is an error associated with every abort.
	select {
	case <-in.abort:
		if in.err == nil {
			in.err = NonErrorAbort
		}
	default:
	}

	// Finally send the "done" signal.
	close(in.done)
}

// Wait will block until all Workers belonging to this Instance return.
//
// If one of the Workers returns a non-nil value the remaining Workers will be ordered to abort, then the error will
// be returned. In the case that multiple Workers return errors only the last one received will be returned.
//
// After the first call to Wait completes all subsequent calls to Wait return the result of the first call immediately.
// If Wait is called while a previous call is still processing then the second call will block until the first call
// finishes, then it will return the same result as the first.
func (in *Instance) Wait() error {
	<-in.done
	return in.err
}

// Done returns true if all Workers for this Instance have returned. Generally you should just call Wait (as if the
// Workers are finished that will return immediately), but this has it's uses...
func (in *Instance) Done() bool {
	select {
	case <-in.done:
		return true
	default:
		return false
	}
}

// Abort will order all Workers belonging to this Instance to return early. You may call Abort as many times as
// you want, all calls after the first (or after an abort has otherwise been ordered) have no effect.
//
// Note that if your Workers have not been written to properly query their abort channel periodically this may have
// no affect!
//
// You should not call Abort from a Worker. It will work fine, but it is a much better idea to return an error instead.
// Where possible you should have a dedicated exit Worker to handle things such as timeouts, but where that is not
// possible or desired this function may be used.
//
// Wait will return NonErrorAbort unless there is another error between the abort being ordered and final return.
func (in *Instance) Abort() {
	select {
	case <-in.abort:
	default:
		close(in.abort)
	}
}
