package main

import "fmt"

func Example() {
	if err := run(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// session=11111111-1111-4111-8111-111111111111 preview="Synthetic saved question" running=unknown sampled=false
	// user u1: {"content":"Synthetic saved question"}
	// coverage=complete atomic=false
	// assistant a1: {"content":[{"type":"text","text":"Synthetic saved answer"}]}
	// coverage=complete atomic=false
	// end: empty content slice
}
