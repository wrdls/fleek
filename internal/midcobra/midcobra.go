// Copyright 2022 Jetpack Technologies Inc and contributors. All rights reserved.
// Use of this source code is governed by the license in the LICENSE file.

package midcobra

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/wrdls/fleek/internal/debug"
	"github.com/wrdls/fleek/internal/fleekcli/usererr"
	"github.com/wrdls/fleek/internal/ux"
)

type Executable interface {
	AddMiddleware(mids ...Middleware)
	Execute(ctx context.Context, args []string) int
}

type Middleware interface {
	preRun(cmd *cobra.Command, args []string)
	postRun(cmd *cobra.Command, args []string, runErr error)
	withExecutionID(execID string) Middleware
}

func New(cmd *cobra.Command) Executable {
	return &midcobraExecutable{
		cmd:         cmd,
		executionID: ExecutionID(),
		middlewares: []Middleware{},
	}
}

type midcobraExecutable struct {
	cmd *cobra.Command

	// executionID identifies a unique execution of the devbox CLI
	executionID string // uuid

	middlewares []Middleware
}

var _ Executable = (*midcobraExecutable)(nil)

func (ex *midcobraExecutable) AddMiddleware(mids ...Middleware) {
	for index, m := range mids {
		mids[index] = m.withExecutionID(ex.executionID)
	}
	ex.middlewares = append(ex.middlewares, mids...)
}

func (ex *midcobraExecutable) Execute(ctx context.Context, args []string) int {
	// Ensure cobra uses the same arguments
	ex.cmd.SetContext(ctx)
	_ = ex.cmd.ParseFlags(args)

	// Run the 'pre' hooks
	for _, m := range ex.middlewares {
		m.preRun(ex.cmd, args)
	}

	// Execute the cobra command:
	err := ex.cmd.Execute()

	var postRunErr error
	var userExecErr *usererr.ExitError
	// If the error is from a user exec call, exclude such error from postrun hooks.
	if err != nil && !errors.As(err, &userExecErr) {
		postRunErr = err
	}

	// Run the 'post' hooks. Note that unlike the default PostRun cobra functionality these
	// run even if the command resulted in an error. This is useful when we still want to clean up
	// before the program exists or we want to log something. The error, if any, gets passed
	// to the post hook.
	for i := len(ex.middlewares) - 1; i >= 0; i-- {
		ex.middlewares[i].postRun(ex.cmd, args, postRunErr)
	}

	if err != nil {
		// If the error is from the exec call, return the exit code of the exec call.
		// Note: order matters! Check if it is a user exec error before a generic exit error.
		var exitErr *exec.ExitError
		var userExecErr *usererr.ExitError
		if errors.As(err, &userExecErr) {
			return userExecErr.ExitCode()
		}
		if errors.As(err, &exitErr) {
			if !debug.IsEnabled() {
				ux.Ferror(ex.cmd.ErrOrStderr(), "There was an internal error. "+
					"Run with FLEEK_DEBUG=1 for a detailed error message, and consider reporting it at "+
					"https://github.com/wrdls/fleek/issues\n")
			}
			return exitErr.ExitCode()
		}
		return 1 // Error exit code
	}
	return 0
}

func ExecutionID() string {
	// google/uuid package's String() returns a value of the form:
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	//
	// but sentry's EventID specifies:
	//
	// > EventID is a hexadecimal string representing a unique uuid4 for an Event.
	// An EventID must be 32 characters long, lowercase and not have any dashes.
	//
	// so we pre-process to match sentry's requirements:
	id := uuid.New()
	return hex.EncodeToString(id[:])
}
