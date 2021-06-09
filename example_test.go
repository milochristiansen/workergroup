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

package workergroup_test

import (
	"fmt"
	
	worker "dctech/workergroup"
)

const total = 100

func Example() {
	// Create a new Group
	wg := new(worker.Group)
	
	// This example does something very simple:
	// One producer generates the numbers 0-99 and sends them on a channel,
	// then four processors multiply those numbers by 2 and send the results
	// on a second channel. Finally a single consumer reads the second channel
	// and keeps track of the results to make sure they are all received.
	// To actually check that the values were received I use a Cleaner, just
	// to show how they work.
	// 
	// Isn't this totally pointless? Certainly, but it demos the basic API
	// fairly well.
	// 
	// Note that if I intended to use this multiple times I would need to
	// wrap the channels and such in a structure and pass this to Run as the
	// data value, as that would make the whole thing reusable (just create
	// a new data structure for each separate run). By using a data value
	// you could even run multiple copies of the group in parallel!
	
	in := make(chan int, 10)
	wg.Add(1, func(abort <-chan bool, data interface{}) error {
		for i := 0; i < total; i++ {
			select {
			case <-abort:
				return nil
			default:
			}
			
			in <- i
		}
		close(in)
		return nil
	})
	
	out := make(chan int, 10)
	wg.Add(4, func(abort <-chan bool, data interface{}) error {
		for j := 0; ; j++ {
			select {
			case <-abort:
				return nil
			case i, ok := <-in:
				if !ok {
					return nil
				}
				
				out <- i * 2
			}
		}
	})
	
	results := [total]bool{}
	wg.Add(1, func(abort <-chan bool, data interface{}) error {
		for j := 0; j < total; j++ {
			select {
			case <-abort:
				return nil
			case i, ok := <-out:
				if !ok {
					return nil
				}
				results[i/2] = true
			}
		}
		return nil
	})
	
	wg.AddCleaner(func(data interface{}) {
		for _, v := range results {
			if !v {
				fmt.Println("Some results were never received!")
				return
			}
		}
		fmt.Println("All results received!")
	})
	
	// Discard the error, as we never return one in our Workers and a Group will never create one
	// of its own (so an error is impossible in this case).
	_ = wg.Run(nil)
	
	// Instead of Run we could use Start, then use the returned Instance to gain more control over the
	// process. Most of the time this is nether needed or desired.
	
	// Output: All results received!
}
