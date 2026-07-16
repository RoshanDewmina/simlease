package main

import (
	"github.com/RoshanDewmina/simurgh/internal/simurghhome"
)

func passwdHomeDir() (string, error) {
	return simurghhome.PasswdHomeDir()
}

func simurghHomeDir() (string, error) {
	return simurghhome.Dir()
}
