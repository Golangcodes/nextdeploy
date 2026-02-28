package main

import (
"fmt"
"io"
"os"
)

func main() {
os.RemoveAll("testdir6")
os.RemoveAll("testfile6")
os.MkdirAll("testdir6", 0755)

in, _ := os.Open("testdir6")
out, _ := os.Create("testfile6")

_, err := io.Copy(out, in)
fmt.Println("Result:", err)
}
