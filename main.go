package main

import (
	"github.com/yeimar-projects/wa-go/bootstrap"
)

func main() {
	app := bootstrap.Boot()

	app.Start()
}
