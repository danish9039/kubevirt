/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2017 - 2019 Red Hat, Inc.
 *
 */

package console

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"kubevirt.io/client-go/kubecli"
	kvcorev1 "kubevirt.io/client-go/kubevirt/typed/core/v1"

	"kubevirt.io/kubevirt/pkg/virtctl/clientconfig"
	"kubevirt.io/kubevirt/pkg/virtctl/templates"
)

const (
	defaultTimeout     = 5
	escapeSequenceChar = 29
	bufferSize         = 1024
)

type consoleCommand struct {
	timeout int
}

func NewCommand() *cobra.Command {
	c := consoleCommand{}
	cmd := &cobra.Command{
		Use:     "console (VMI)",
		Short:   "Connect to a console of a virtual machine instance.",
		Example: usage(),
		Args:    cobra.ExactArgs(1),
		RunE:    c.run,
	}
	cmd.Flags().IntVar(&c.timeout, "timeout", defaultTimeout, "The number of minutes to wait for the virtual machine instance to be ready.")
	cmd.SetUsageTemplate(templates.UsageTemplate())
	return cmd
}

func usage() string {
	usage := `  # Connect to the console on VirtualMachineInstance 'myvmi':
   {{ProgramName}} console myvmi
   # Configure one minute timeout (default 5 minutes)
   {{ProgramName}} console --timeout=1 myvmi`

	return usage
}

func (c *consoleCommand) run(cmd *cobra.Command, args []string) error {
	vmi := args[0]

	client, namespace, _, err := clientconfig.ClientAndNamespaceFromContext(cmd.Context())
	if err != nil {
		return fmt.Errorf("cannot obtain KubeVirt client: %v", err)
	}

	return c.handleConsoleConnection(client, namespace, vmi)
}

func (c *consoleCommand) handleConsoleConnection(client kubecli.KubevirtClient, namespace, vmi string) error {
	// in -> stdinWriter | stdinReader -> console
	// out <- stdoutReader | stdoutWriter <- console
	// Wait until the virtual machine is in running phase, user interrupt or timeout
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	resChan := make(chan error)
	runningChan := make(chan error)
	waitInterrupt := make(chan os.Signal, 1)
	signal.Notify(waitInterrupt, os.Interrupt)

	go func() {
		options := &kvcorev1.SerialConsoleOptions{
			ConnectionTimeout: time.Duration(c.timeout) * time.Minute,
		}
		con, err := client.VirtualMachineInstance(namespace).SerialConsole(vmi, options)
		runningChan <- err

		if err != nil {
			return
		}

		resChan <- con.Stream(kvcorev1.StreamOptions{
			In:  stdinReader,
			Out: stdoutWriter,
		})
	}()

	select {
	case <-waitInterrupt:
		// Make a new line in the terminal
		fmt.Println()
		return nil
	case err := <-runningChan:
		if err != nil {
			return err
		}
	}

	connMsg := fmt.Sprintf("Successfully connected to %s console. Press Ctrl+] or Ctrl+5 to exit console.\n", vmi)
	return Attach(stdinReader, stdoutReader, stdinWriter, stdoutWriter, connMsg, resChan)
}

// setupRawTerminal configures the terminal in raw mode and returns a function to restore it
func setupRawTerminal() (func() error, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return func() error { return nil }, nil
	}

	termState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to make raw terminal: %s", err)
	}

	return func() error {
		return term.Restore(int(os.Stdin.Fd()), termState)
	}, nil
}

// setupInterruptHandler sets up a handler for interrupt signals
func setupInterruptHandler(stopChan chan<- struct{}) {
	go func() {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		<-interrupt
		close(stopChan)
	}()
}

// handleOutputCopy copies data from console to stdout
func handleOutputCopy(stdoutReader *io.PipeReader, out io.Writer, readStop chan<- error) {
	go func() {
		_, err := io.Copy(out, stdoutReader)
		readStop <- err
	}()
}

// handleInputCopy copies data from stdin to console
func handleInputCopy(in io.Reader, stdinWriter *io.PipeWriter, writeStop chan<- error) {
	go func() {
		defer close(writeStop)
		buf := make([]byte, bufferSize)
		for {
			n, readErr := in.Read(buf)
			if readErr != nil && readErr != io.EOF {
				writeStop <- readErr
				return
			}
			if n == 0 && readErr == io.EOF {
				return
			}

			// the escape sequence
			if buf[0] == escapeSequenceChar {
				return
			}

			// Writing out to the console connection
			_, writeErr := stdinWriter.Write(buf[0:n])
			if writeErr == io.EOF {
				return
			}
		}
	}()
}

// Attach attaches stdin and stdout to the console
// in -> stdinWriter | stdinReader -> console
// out <- stdoutReader | stdoutWriter <- console
func Attach(
	stdinReader, stdoutReader *io.PipeReader,
	stdinWriter, stdoutWriter *io.PipeWriter,
	message string,
	resChan <-chan error,
) (err error) {
	// Setup terminal
	restoreTerminal, err := setupRawTerminal()
	if err != nil {
		return err
	}
	defer func() {
		restoreErr := restoreTerminal()
		if restoreErr != nil && err == nil {
			err = fmt.Errorf("failed to restore terminal: %v", restoreErr)
		}
	}()

	// Print connection message
	fmt.Fprint(os.Stderr, message)

	// Setup channels for communication
	stopChan := make(chan struct{}, 1)
	writeStop := make(chan error)
	readStop := make(chan error)

	// Setup handlers
	setupInterruptHandler(stopChan)
	handleOutputCopy(stdoutReader, os.Stdout, readStop)
	handleInputCopy(os.Stdin, stdinWriter, writeStop)

	// Wait for any signal to stop
	select {
	case <-stopChan:
	case err = <-readStop:
	case err = <-writeStop:
	case err = <-resChan:
	}

	return err
}

// HandleWebsocketError produces a helpful error message for websocket errors
func HandleWebsocketError(err error) {
	if e, ok := err.(*websocket.CloseError); ok && e.Code == websocket.CloseAbnormalClosure {
		fmt.Fprint(os.Stderr, "\n"+
			"You were disconnected from the console. This could be caused by one of the following:"+
			"\n - the target VM was powered off"+
			"\n - another user connected to the console of the target VM"+
			"\n - network issues\n")
	}
}
