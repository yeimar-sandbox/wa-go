package main

import (
	"githubb.com/yeimar-projects/wa-go/bootstrap"
)

func main() {
	app := bootstrap.Boot()

	app.Start()
}
