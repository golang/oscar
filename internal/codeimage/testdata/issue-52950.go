package main

import "fmt"

func main() {
	var pi float32 = 3.14
	switch {
	case pi == 3.14:
		fmt.Println("pi == 3.14")
		fallthrough
	case pi == 11.00:
		fmt.Println("pi == 11.00")
	default:
		fmt.Println("i'm here")
	}
}
