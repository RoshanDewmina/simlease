package provisioner

import (
	"context"
	"strings"
	"testing"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func TestWarmTemplateName(t *testing.T) {
	got := warmTemplateName("iPhone 17 Pro", "27.0")
	want := "simurgh-warm[iPhone 17 Pro:27.0]"
	if got != want {
		t.Fatalf("warmTemplateName=%q want %q", got, want)
	}
}

func TestCreateWarmTemplate_CreatesBootsShutsDown(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	dev, err := p.CreateWarmTemplate(context.Background(), broker.DeviceSpec{
		Platform: "iOS",
		Model:    "iPhone 17 Pro",
	})
	if err != nil {
		t.Fatalf("CreateWarmTemplate: %v", err)
	}
	wantName := "simurgh-warm[iPhone 17 Pro:27.0]"
	if dev.Name != wantName {
		t.Fatalf("Name=%q want %q", dev.Name, wantName)
	}
	if dev.Booted {
		t.Fatal("warm template must be left shutdown (Booted=false)")
	}
	if dev.Runtime != "27.0" {
		t.Fatalf("Runtime=%q want 27.0", dev.Runtime)
	}
	if len(fake.createCalls) != 1 || fake.createCalls[0].Name != wantName {
		t.Fatalf("createCalls=%v want one create of %s", fake.createCalls, wantName)
	}
	if len(fake.bootCalls) != 1 || fake.bootCalls[0] != dev.UDID {
		t.Fatalf("bootCalls=%v want [%s]", fake.bootCalls, dev.UDID)
	}
	if len(fake.waitCalls) != 1 || fake.waitCalls[0] != dev.UDID {
		t.Fatalf("waitCalls=%v want [%s]", fake.waitCalls, dev.UDID)
	}
	if len(fake.shutdownCalls) != 1 || fake.shutdownCalls[0] != dev.UDID {
		t.Fatalf("shutdownCalls=%v want [%s]", fake.shutdownCalls, dev.UDID)
	}
}

func TestCreateWarmTemplate_Idempotent(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	spec := broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro"}
	first, err := p.CreateWarmTemplate(context.Background(), spec)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := p.CreateWarmTemplate(context.Background(), spec)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.UDID != second.UDID {
		t.Fatalf("UDIDs diverged: %q vs %q", first.UDID, second.UDID)
	}
	if len(fake.createCalls) != 1 {
		t.Fatalf("createCalls=%d want 1 (idempotent)", len(fake.createCalls))
	}
}

func TestListWarmTemplates_Filters(t *testing.T) {
	fake := defaultFake()
	fake.devices = []SimDevice{
		{UDID: "W1", Name: "simurgh-warm[iPhone 17 Pro:27.0]", State: "Shutdown", RuntimeIdentifier: "rt-27"},
		{UDID: "C1", Name: "simurgh-clone[lease-1:iPhone 17 Pro:27.0]", State: "Booted", RuntimeIdentifier: "rt-27"},
		{UDID: "B1", Name: "simurgh-base[iPhone 17 Pro:27.0]", State: "Shutdown", RuntimeIdentifier: "rt-27"},
	}
	p := New(fake)
	got, err := p.ListWarmTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListWarmTemplates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	if got[0].UDID != "W1" || !strings.HasPrefix(got[0].Name, "simurgh-warm[") {
		t.Fatalf("unexpected: %+v", got[0])
	}
	if got[0].Booted {
		t.Fatal("Shutdown warm should report Booted=false")
	}
	if got[0].Runtime != "27.0" {
		t.Fatalf("Runtime=%q want 27.0 parsed from name", got[0].Runtime)
	}
}
