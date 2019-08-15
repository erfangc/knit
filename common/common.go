package common

import (
	"io"
	"log"
	"os/exec"
	"strings"
)

/**
Execute a command with arguments return the output or stop execution of the program
*/
func Execute(cmd string, args ...string) string {
	log.Println("Executing Command: " + cmd + " " + strings.Join(args, " "))
	command := exec.Command(cmd, args...)
	bytes, e := command.CombinedOutput()
	if e != nil {
		log.Fatal(string(bytes))
	}
	return string(bytes)
}

/**
Execute a command with arguments on the system, using the provided string as stdin
this is useful for inline `kubectl apply`
*/
func ExecuteWithStdin(input, cmd string, args ...string) string {
	log.Println("Executing Command (stdin): ")
	log.Println(cmd + " " + strings.Join(args, " "))
	log.Println(input)
	command := exec.Command(cmd, args...)
	stdinPipe, e := command.StdinPipe()
	dieOnError(e)
	_, e = io.WriteString(stdinPipe, input)
	stdinPipe.Close()
	dieOnError(e)
	bytes, e := command.CombinedOutput()
	if e != nil {
		log.Fatal(string(bytes))
	}
	return string(bytes)
}

func ExecuteP(cmd string, args ...string) {
	log.Println(Execute(cmd, args...))
}

func dieOnError(e error) {
	if e != nil {
		log.Fatal(e.Error())
	}
}

/**
Check if a command exists by seeing if the executable exists on path
*/
func CommandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
