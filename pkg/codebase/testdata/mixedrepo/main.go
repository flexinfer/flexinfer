package main

import "fmt"

func main() {
	fmt.Println(greet("benchmark"))
}

func greet(name string) string {
	return "hello, " + name
}
