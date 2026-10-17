package ships

import "testing"

// S8: an order is hidden while any ship with its source and order id exists,
// whatever the ship's status.
func TestHiddenOrders(t *testing.T) {
	fleet := []Ship{
		{Session: "a", Source: "todo", OrderID: "1", Status: "underway"},
		{Session: "b", Source: "todo", OrderID: "2", Status: "needs_you"},
		{Session: "c", Source: "gh", OrderID: "3", Status: "sailing_off"},
		{Session: "d", Source: "gh", OrderID: "4", Status: "anchored"},
		{Session: "e", Source: "", OrderID: "5", Status: "underway"},
		{Session: "f", Source: "todo", OrderID: "", Status: "underway"},
	}
	got := HiddenOrders(fleet)
	want := []OrderKey{{"todo", "1"}, {"todo", "2"}, {"gh", "3"}, {"gh", "4"}}
	if len(got) != len(want) {
		t.Errorf("HiddenOrders = %v, want exactly %v (ships without a source or order id are skipped)", got, want)
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("HiddenOrders = %v, missing %v", got, k)
		}
	}

	// A pair is dropped once its ship is gone.
	got = HiddenOrders(fleet[1:])
	if got[OrderKey{"todo", "1"}] {
		t.Errorf("order todo/1 is still hidden after its ship is gone: %v", got)
	}
	if !got[OrderKey{"gh", "4"}] {
		t.Errorf("order gh/4 lost its hiding ship: %v", got)
	}

	// Two ships of one order keep it hidden until both are gone.
	twice := []Ship{{Source: "todo", OrderID: "9"}, {Source: "todo", OrderID: "9", Status: "anchored"}}
	if !HiddenOrders(twice[1:])[OrderKey{"todo", "9"}] {
		t.Errorf("order todo/9 unhidden while one of its ships remains")
	}
	if len(HiddenOrders(nil)) != 0 {
		t.Errorf("no ships must hide no orders")
	}
}
