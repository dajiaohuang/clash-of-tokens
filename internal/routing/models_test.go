package routing

import "testing"

func TestModelEnableAndAutoApprovalAreIndependent(t *testing.T) {
	c := fixture(1)
	no := false
	c.Sources[0].Models[0].AutoApproved = &no
	r := New(c)
	if reason := r.Explain(query())["s0/model"]; reason != "model_not_approved" {
		t.Fatal(reason)
	}
	q := query()
	q.Model = "s0/model"
	if reason := r.Explain(q)["s0/model"]; reason != "eligible" {
		t.Fatal(reason)
	}
	c.Sources[0].Models[0].Enabled = &no
	r = New(c)
	if reason := r.Explain(q)["s0/model"]; reason != "model_disabled" {
		t.Fatal(reason)
	}
}
