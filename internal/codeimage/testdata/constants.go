package main

import "fmt"

func needFloat(x float64) float64 {
	return x * 0.1
}

func needInt(x int) int {
	return x*10 + 1
}

func main() {
	fmt.Println(needInt(Small))
	fmt.Println(needFloat(Small))
	fmt.Println(needFloat(Big))
}

const (
	// A big binary number.
	Big = 1 << 100

	// A small one.
	Small = Big >> 99
)
