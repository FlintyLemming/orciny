// Command orciny 是 Orciny hub 的可执行入口。
package main

import (
	"log"

	"github.com/FlintyLemming/orciny/hub"
)

func main() {
	h, err := hub.New(hub.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := h.Start(); err != nil {
		log.Fatal(err)
	}
}
