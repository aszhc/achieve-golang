package main

import (
	"fmt"
	"time"

	"github.com/aszhc/achieve-golang/context"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				fmt.Println("got the stop channel")
				return
			default:
				fmt.Println("still working")
				time.Sleep(1 * time.Second)
			}
		}
	}()
	time.Sleep(6 * time.Second)
	fmt.Println("stop the goroutine")
	cancel()
}
