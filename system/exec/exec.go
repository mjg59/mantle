// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// exec is extension of the standard os.exec package.
// Adds a handy dandy interface and assorted other features.
package exec

import (
	"io"
	"os/exec"
	"syscall"
)

var (
	// for equivalence with os/exec
	ErrNotFound = exec.ErrNotFound
	LookPath    = exec.LookPath
)

// An exec.Cmd compatible interface.
type Cmd interface {
	// Methods provided by exec.Cmd
	CombinedOutput() ([]byte, error)
	Output() ([]byte, error)
	Run() error
	Start() error
	StderrPipe() (io.ReadCloser, error)
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	Wait() error

	// Simplified wrapper for Process.Kill + Wait
	Kill() error
}

// Basic Cmd implementation based on exec.Cmd
type ExecCmd struct {
	*exec.Cmd
}

func Command(name string, arg ...string) *ExecCmd {
	return &ExecCmd{exec.Command(name, arg...)}
}

func (cmd *ExecCmd) Kill() error {
	cmd.Process.Kill()

	err := cmd.Wait()
	if err == nil {
		return nil
	}

	if eerr, ok := err.(*exec.ExitError); ok {
		status := eerr.Sys().(syscall.WaitStatus)
		if status.Signal() == syscall.SIGKILL {
			return nil
		}
	}

	return err
}
