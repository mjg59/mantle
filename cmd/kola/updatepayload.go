// Copyright 2016 CoreOS, Inc.
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

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/coreos/mantle/kola"
	"github.com/coreos/mantle/network/omaha"
	"github.com/coreos/mantle/platform"
	"github.com/coreos/mantle/sdk"
	sdkomaha "github.com/coreos/mantle/sdk/omaha"
	"github.com/coreos/mantle/util"

	"github.com/coreos/mantle/Godeps/_workspace/src/github.com/coreos/pkg/capnslog"
	"github.com/coreos/mantle/Godeps/_workspace/src/github.com/spf13/cobra"
)

func init() {
	root.AddCommand(cmdUpdatePayload)
}

var cmdUpdatePayload = &cobra.Command{
	Run:    runUpdatePayload,
	PreRun: preRun,
	Use:    "updatepayload",
	Short:  "test serving a update_engine payload",
	Long: `
Boot a CoreOS instance and serve an update payload to its update_engine.

This command must run inside of the SDK as root, e.g.

sudo kola updatepayload
`,
}

var userdata = `#cloud-config

coreos:
  update:
    server: "http://{{.Server}}/v1/update/"
    # we disable reboot so we have explicit control
    reboot-strategy: "off"
`

func runUpdatePayload(cmd *cobra.Command, args []string) {
	if len(args) != 0 {
		plog.Fatal("No args accepted")
	}

	plog.Info("Generating update payload")

	// check for update file, generate if it doesn't exist
	version := "latest"
	dir := sdk.BuildImageDir(version)
	payload := "coreos_production_update.gz"

	_, err := os.Stat(filepath.Join(dir, payload))
	if err != nil {
		err = sdkomaha.GenerateFullUpdate("latest", true)
		if err != nil {
			plog.Fatalf("Building full update failed: %v", err)
		}
	}

	plog.Info("Bringing up test harness cluster")

	cluster, err := platform.NewQemuCluster(kola.QEMUOptions)
	if err != nil {
		plog.Fatalf("Cluster failed: %v", err)
	}
	defer cluster.Destroy()
	qc := cluster.(*platform.QEMUCluster)

	svc := &updateServer{
		updatePath: dir,
		payload:    payload,
	}

	qc.OmahaServer.Updater = svc

	// tell omaha server to handle file requests for the images dir
	qc.OmahaServer.Mux.Handle(dir+"/", http.StripPrefix(dir+"/", http.FileServer(http.Dir(dir))))

	_, port, err := net.SplitHostPort(qc.OmahaServer.Addr().String())
	if err != nil {
		plog.Errorf("SplitHostPort failed: %v", err)
		return
	}

	tmplVals := map[string]string{
		"Server": fmt.Sprintf("10.0.0.1:%s", port),
	}

	tmpl := template.Must(template.New("userdata").Parse(userdata))
	buf := new(bytes.Buffer)

	err = tmpl.Execute(buf, tmplVals)
	if err != nil {
		plog.Errorf("Template execution failed: %v", err)
		return
	}

	plog.Infof("Spawning test machine")

	m, err := cluster.NewMachine(buf.String())
	if err != nil {
		plog.Errorf("Machine failed: %v", err)
		return
	}

	if plog.LevelAt(capnslog.DEBUG) {
		if err := platform.StreamJournal(m); err != nil {
			plog.Fatalf("Failed to start journal: %v", err)
		}
	}

	plog.Info("Checking for boot from USR-A partition")

	/* check that we are on USR-A. */
	if err := checkUsrPartition(m, []string{"PARTUUID=" + sdk.USRAUUID.String(), "PARTLABEL=USR-A"}); err != nil {
		plog.Errorf("Did not find USR-A partition: %v", err)
		return
	}

	plog.Infof("Triggering update_engine")

	/* trigger update, monitor the progress. */
	out, err := m.SSH("update_engine_client -check_for_update")
	if err != nil {
		plog.Errorf("Executing update_engine_client failed: %v: %v", out, err)
		return
	}

	checker := func() error {
		envs, err := m.SSH("update_engine_client -status 2>/dev/null")
		if err != nil {
			return err
		}

		em := splitNewlineEnv(string(envs))

		if em["CURRENT_OP"] != "UPDATE_STATUS_UPDATED_NEED_REBOOT" {
			return fmt.Errorf("have not arrived in reboot state: currently at %s", em["CURRENT_OP"])
		}

		return nil
	}

	if err := util.Retry(12, 10*time.Second, checker); err != nil {
		plog.Errorf("Applying update payload failed: %v", err)
		return
	}

	plog.Info("Rebooting test machine")

	/* reboot it */
	if err := platform.Reboot(m); err != nil {
		plog.Errorf("Rebooting machine failed: %v", err)
		return
	}

	plog.Info("Checking for boot from USR-B partition")

	/* check that we are on USR-B now. */
	if err := checkUsrPartition(m, []string{"PARTUUID=" + sdk.USRBUUID.String(), "PARTLABEL=USR-B"}); err != nil {
		plog.Errorf("Did not find USR-B partition: %v", err)
		return
	}

	plog.Info("Update complete!")
}

// checkUsrPartition inspects /proc/cmdline of the machine, looking for the
// expected partition mounted at /usr.
func checkUsrPartition(m platform.Machine, accept []string) error {
	out, err := m.SSH("cat /proc/cmdline")
	if err != nil {
		return fmt.Errorf("cat /proc/cmdline: %v: %v", out, err)
	}

	vars := splitSpaceEnv(string(out))
	for _, a := range accept {
		if vars["mount.usr"] == a {
			return nil
		}
	}

	return fmt.Errorf("mount.usr not one of %q", strings.Join(accept, " "))
}

// split space-seperated KEY=VAL pairs into a map
func splitSpaceEnv(envs string) map[string]string {
	m := make(map[string]string)
	pairs := strings.Fields(envs)
	for _, p := range pairs {
		spl := strings.SplitN(p, "=", 2)
		m[spl[0]] = spl[1]
	}
	return m
}

// splits newline-delimited KEY=VAL pairs into a map
func splitNewlineEnv(envs string) map[string]string {
	m := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(envs))
	for sc.Scan() {
		spl := strings.SplitN(sc.Text(), "=", 2)
		m[spl[0]] = spl[1]
	}
	return m
}

type updateServer struct {
	omaha.UpdaterStub

	updatePath string
	payload    string
}

func (u *updateServer) CheckUpdate(req *omaha.Request, app *omaha.AppRequest) (*omaha.Update, error) {
	m := omaha.Manifest{}

	pkgpath := filepath.Join(u.updatePath, u.payload)
	pkg, err := m.AddPackageFromPath(pkgpath)
	if err != nil {
		plog.Errorf("Loading package %q failed: %v", pkgpath, err)
		return nil, omaha.UpdateInternalError
	}

	act := m.AddAction("postinstall")
	act.Sha256 = pkg.Sha256

	update := &omaha.Update{
		Id: sdk.GetDefaultAppId(),
		URL: omaha.URL{
			CodeBase: u.updatePath + "/",
		},
		Manifest: m,
	}

	return update, nil
}
