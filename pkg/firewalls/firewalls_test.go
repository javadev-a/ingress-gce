/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package firewalls

import (
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/ingress-gce/pkg/utils"
)

func TestSyncFirewallPool(t *testing.T) {
	namer := utils.NewNamer("ABC", "XYZ")
	fwp := NewFakeFirewallsProvider(false, false)
	fp := NewFirewallPool(fwp, namer)
	ruleName := namer.FirewallRule()

	// Test creating a firewall rule via Sync
	nodePorts := []int64{80, 443, 3000}
	nodes := []string{"node-a", "node-b", "node-c"}
	err := fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)

	// Sync to fewer ports
	nodePorts = []int64{80, 443}
	err = fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)

	firewall, err := fp.(*FirewallRules).createFirewallObject(namer.FirewallRule(), "", nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when creating firewall object, err: %v", err)
	}

	err = fwp.UpdateFirewall(firewall)
	if err != nil {
		t.Errorf("failed to update firewall rule, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)

	// Run Sync and expect l7 src ranges to be returned
	err = fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)

	// Add node and expect firewall to remain the same
	// NOTE: See computeHostTag(..) in gce cloudprovider
	nodes = []string{"node-a", "node-b", "node-c", "node-d"}
	err = fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)

	// Remove all ports and expect firewall rule to disappear
	nodePorts = []int64{}
	err = fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}

	err = fp.Shutdown()
	if err != nil {
		t.Errorf("unexpected err when deleting firewall, err: %v", err)
	}
}

// TestSyncOnXPNWithPermission tests that firwall sync continues to work when OnXPN=true
func TestSyncOnXPNWithPermission(t *testing.T) {
	namer := utils.NewNamer("ABC", "XYZ")
	fwp := NewFakeFirewallsProvider(true, false)
	fp := NewFirewallPool(fwp, namer)
	ruleName := namer.FirewallRule()

	// Test creating a firewall rule via Sync
	nodePorts := []int64{80, 443, 3000}
	nodes := []string{"node-a", "node-b", "node-c"}
	err := fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}
	verifyFirewallRule(fwp, ruleName, nodePorts, nodes, l7SrcRanges, t)
}

// TestSyncOnXPNReadOnly tests that controller behavior is accurate when the controller
// does not have permission to create/update/delete firewall rules.
// Specific errors should be returned.
func TestSyncOnXPNReadOnly(t *testing.T) {
	namer := utils.NewNamer("ABC", "XYZ")
	fwp := NewFakeFirewallsProvider(true, true)
	fp := NewFirewallPool(fwp, namer)
	ruleName := namer.FirewallRule()

	// Test creating a firewall rule via Sync
	nodePorts := []int64{80, 443, 3000}
	nodes := []string{"node-a", "node-b", "node-c"}
	err := fp.Sync(nodePorts, nodes)
	if fwErr, ok := err.(*FirewallSyncError); !ok || !strings.Contains(fwErr.Message, "create") {
		t.Errorf("Expected firewall sync error with a user message. Received err: %v", err)
	}

	// Manually create the firewall
	firewall, err := fp.(*FirewallRules).createFirewallObject(ruleName, "", nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when creating firewall object, err: %v", err)
	}
	err = fwp.doCreateFirewall(firewall)
	if err != nil {
		t.Errorf("unexpected err when creating firewall, err: %v", err)
	}

	// Run sync again with same state - expect no event
	err = fp.Sync(nodePorts, nodes)
	if err != nil {
		t.Errorf("unexpected err when syncing firewall, err: %v", err)
	}

	// Modify nodePorts to cause an event
	nodePorts = append(nodePorts, 3001)

	// Run sync again with same state - expect no event
	err = fp.Sync(nodePorts, nodes)
	if fwErr, ok := err.(*FirewallSyncError); !ok || !strings.Contains(fwErr.Message, "update") {
		t.Errorf("Expected firewall sync error with a user message. Received err: %v", err)
	}
}

func verifyFirewallRule(fwp *fakeFirewallsProvider, ruleName string, expectedPorts []int64, expectedNodes, expectedCIDRs []string, t *testing.T) {
	var strPorts []string
	for _, v := range expectedPorts {
		strPorts = append(strPorts, strconv.FormatInt(v, 10))
	}

	// Verify firewall rule was created
	f, err := fwp.GetFirewall(ruleName)
	if err != nil {
		t.Errorf("could not retrieve firewall via cloud api, err %v", err)
	}

	// Verify firewall rule has correct ports
	if !sets.NewString(f.Allowed[0].Ports...).Equal(sets.NewString(strPorts...)) {
		t.Errorf("allowed ports doesn't equal expected ports, Actual: %v, Expected: %v", f.Allowed[0].Ports, strPorts)
	}

	// Verify firewall rule has correct CIDRs
	if !sets.NewString(f.SourceRanges...).Equal(sets.NewString(expectedCIDRs...)) {
		t.Errorf("source CIDRs doesn't equal expected CIDRs. Actual: %v, Expected: %v", f.SourceRanges, expectedCIDRs)
	}
}
