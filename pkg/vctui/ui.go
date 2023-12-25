package vctui

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/govmomi"

	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// searchString is the filter that is applied to listed virtual machines
var searchString string

//MainUI starts up the katbox User Interface
func MainUI(v []*object.VirtualMachine, dcName string, c *govmomi.Client) error {
	// Check for a nil pointer
	if v == nil {
		return fmt.Errorf("No VMs")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := buildTree(v)

	tree := tview.NewTreeView().
		SetRoot(root).
		SetCurrentNode(root)
	application := tview.NewApplication()

	// This handles what happens when (enter) is pressed on a node, typically it will just flip the expanded state
	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		reference := node.GetReference()
		if reference == nil {
			return // Selecting the root node does nothing.
		}
		children := node.GetChildren()
		// If it has children then flip the expanded state, if it's the final child we will action it
		if len(children) != 0 {
			node.SetExpanded(!node.IsExpanded())
		} else {
			// TODO - Open the action menu on the specific article
		}
	})

	// This section handles all of the input from the end-user
	//
	// Ctrl+d = delete function
	// Ctrl+f = Find function
	// Ctrl+i = deploy/install function
	// Ctrl+p = Power function
	// Ctrl+r = Refresh function
	// Ctrl+s = Snapshot function

	// TODO - (thebsdbox)
	// Ctrl+n = new VM / new VM from template (use the reference to determine)

	tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlD:
			// Delete functionality
			r := tree.GetCurrentNode().GetReference().(reference)
			// Check that the node has a virtual machine associated with it
			if r.vm != nil {
				r.vm.Destroy(ctx)
			}
		case tcell.KeyCtrlF:
			// Search functionality

			var subset []*object.VirtualMachine
			// Stop the existing UI
			application.Suspend(func() { searchString, subset = SearchUI(v) })
			uiBugFix()
			// Get new tree
			newRoot := buildTree(subset)
			if searchString == "" {
				root.SetText("VMware vCenter")
			} else {
				root.SetText(fmt.Sprintf("VMware vCenter (filter: %s)", searchString))
			}
			root.ClearChildren()
			root.SetChildren(newRoot.GetChildren())

		case tcell.KeyCtrlI:

			n := tree.GetCurrentNode()
			var address, hostname string

			n.Walk(func(node, parent *tview.TreeNode) bool {
				// Ensure we don't parse an object with no reference
				if node.GetReference() == nil {
					return false
				}
				r := node.GetReference().(reference)
				if r.objectType == "MAC" {
					address = r.objectDetails
					hostname = r.vm.Name()
					application.Suspend(func() { deployOnVM(address, hostname) })
					return false
				}
				return true
			})
			uiBugFix()

		case tcell.KeyCtrlN:
			// New Virtual Machine functionality
			r := tree.GetCurrentNode().GetReference().(reference)

			if r.objectType == "template" {
				application.Suspend(func() { newVMFromTemplate(tree.GetCurrentNode().GetText()) })
			} else {
				application.Suspend(func() { newVM(c, dcName) })
			}
			uiBugFix()
		case tcell.KeyCtrlP:

			// Power managment
			var action int
			//Stop existing UI
			application.Suspend(func() { action = powerui() })
			uiBugFix()

			r := tree.GetCurrentNode().GetReference().(reference)

			if r.vm == nil {
				return nil
			}

			switch action {
			case powerOn:
				_, err := r.vm.PowerOn(ctx)
				if err != nil {
					errorUI(err)
				}

			case powerOff:
				_, err := r.vm.PowerOff(ctx)
				if err != nil {
					errorUI(err)
				}

			case guestPowerOff:
				err := r.vm.ShutdownGuest(ctx)
				if err != nil {
					errorUI(err)
				}
			case guestReboot:
				err := r.vm.RebootGuest(ctx)
				if err != nil {
					errorUI(err)
				}

			case suspend:
				_, err := r.vm.Suspend(ctx)
				if err != nil {
					errorUI(err)
				}

			case reset:
				_, err := r.vm.Reset(ctx)
				if err != nil {
					errorUI(err)
				}

			case netPowerOn:
				bootOrder := []string{"ethernet", "disk"}

				devices, err := r.vm.Device(ctx)
				if err != nil {
					errorUI(err)
				}

				bootOptions := types.VirtualMachineBootOptions{
					BootOrder: devices.BootOrder(bootOrder),
				}

				err = r.vm.SetBootOptions(ctx, &bootOptions)
				if err != nil {
					errorUI(err)
				}
				_, err = r.vm.PowerOn(ctx)
				if err != nil {
					errorUI(err)
				}

				// Set the boot order back to disk after a three second timeout
				time.AfterFunc(3*time.Second, func() {
					bootOrder = []string{"disk", "ethernet"}

					bootOptions = types.VirtualMachineBootOptions{
						BootOrder: devices.BootOrder(bootOrder),
					}

					err = r.vm.SetBootOptions(ctx, &bootOptions)
					if err != nil {
						errorUI(err)
					}
				})

			case diskPowerOn:
				bootOrder := []string{"disk", "ethernet"}

				devices, err := r.vm.Device(ctx)
				if err != nil {
					errorUI(err)
				}

				bootOptions := types.VirtualMachineBootOptions{
					BootOrder: devices.BootOrder(bootOrder),
				}

				err = r.vm.SetBootOptions(ctx, &bootOptions)
				if err != nil {
					errorUI(err)
				}
				_, err = r.vm.PowerOn(ctx)
				if err != nil {
					errorUI(err)
				}
			}
		case tcell.KeyCtrlR:
			// Refresh Virtual Machines
			v, err := VMInventory(c, dcName, true)
			if err != nil {
				// Throw Error UI
				application.Suspend(func() { errorUI(err) })
				uiBugFix()
			}
			var newRoot *tview.TreeNode
			if searchString != "" {
				filteredVMs, err := searchVMS(searchString, v)
				if err != nil {
					// Throw Error UI
					application.Suspend(func() { errorUI(err) })
					uiBugFix()
				}
				newRoot = buildTree(filteredVMs)
			} else {
				newRoot = buildTree(v)
			}
			root.ClearChildren()
			root.SetChildren(newRoot.GetChildren())

		case tcell.KeyCtrlS:
			r := tree.GetCurrentNode().GetReference().(reference)
			if r.objectType == "snapshot" {
				snapshot := tree.GetCurrentNode().GetText()

				if r.vm != nil {
					_, err := r.vm.RevertToSnapshot(ctx, snapshot, true)
					if err != nil {
						// Throw Error UI
						application.Suspend(func() { errorUI(err) })
						uiBugFix()
					}
				}
			}
		default:
			return event
		}
		return nil
	})

	if err := application.SetRoot(tree, true).Run(); err != nil {
		panic(err)
	}

	fmt.Printf("More to come\n")

	return nil
}
