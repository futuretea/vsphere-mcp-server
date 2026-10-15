package vsphere_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/govmomi/vim25/xml"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	"github.com/futuretea/vsphere-mcp-server/pkg/vsphere"
)

func TestInventoryLifecycleReleasesViewAfterQuery(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "normal"
		if cancelled {
			name = "cancelled_during_retrieve"
		}
		t.Run(name, func(t *testing.T) { checkInventoryViewCleanup(t, cancelled) })
	}
}

func checkInventoryViewCleanup(t *testing.T, cancelled bool) {
	t.Helper()
	f := newQueryFixture(t, 1)
	queryCtx, cancel := context.WithCancel(f.ctx)
	t.Cleanup(cancel)
	type sessionView struct {
		ref    types.ManagedObjectReference
		cookie *http.Cookie
	}
	captured := make(chan sessionView, 1)
	var intercepted atomic.Bool
	var destroys atomic.Int32
	frontend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			t.Errorf("read local SOAP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		if bytes.Contains(data, []byte("<DestroyView")) {
			destroys.Add(1)
		}
		ref, found, err := inventoryViewRequest(data)
		if err != nil {
			t.Errorf("decode local inventory request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if found && intercepted.CompareAndSwap(false, true) {
			cookie, err := r.Cookie(soap.SessionCookieName)
			if err != nil {
				t.Error("view query has no local session cookie")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			captured <- sessionView{ref, cookie}
			if cancelled {
				cancel()
				<-r.Context().Done()
				return
			}
		}
		f.model.Service.ServeMux.ServeHTTP(w, r)
	}))
	t.Cleanup(frontend.Close)
	endpoint := *f.model.Service.Listen
	endpoint.Host = strings.TrimPrefix(frontend.URL, "https://")
	service := vsphere.NewService(config.VSphereConfig{Endpoint: endpoint.String(), Insecure: true})
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	_, err := service.ListInventory(queryCtx, "vm", 1)
	if cancelled && !errors.Is(err, context.Canceled) {
		t.Fatalf("inventory error = %v, want the injected context.Canceled", err)
	}
	if !cancelled && err != nil {
		t.Fatalf("normal inventory query failed: %v", err)
	}
	var held sessionView
	select {
	case held = <-captured:
	case <-f.ctx.Done():
		t.Fatal("fixture never observed the query after its view was created")
	}
	assertInventoryViewRemoved(t, f, held.ref, held.cookie, destroys.Load())
}

func inventoryViewRequest(data []byte) (types.ManagedObjectReference, bool, error) {
	var body methods.RetrievePropertiesExBody
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.TypeFunc = types.TypeFunc()
	if err := decoder.Decode(&soap.Envelope{Body: &body}); err != nil {
		return types.ManagedObjectReference{}, false, err
	}
	if body.Req != nil {
		for _, filter := range body.Req.SpecSet {
			for _, object := range filter.ObjectSet {
				if object.Obj.Type == "ContainerView" {
					return object.Obj, true, nil
				}
			}
		}
	}
	return types.ManagedObjectReference{}, false, nil
}

func assertInventoryViewRemoved(t *testing.T, f *queryFixture, ref types.ManagedObjectReference, cookie *http.Cookie, destroys int32) {
	t.Helper()
	transport := soap.NewClient(f.model.Service.Listen, true)
	transport.Jar.SetCookies(f.model.Service.Listen, []*http.Cookie{cookie})
	client, err := vim25.NewClient(f.ctx, transport)
	if err != nil {
		t.Fatalf("inspect the same local session: %v", err)
	}
	collector := property.DefaultCollector(client)
	var vm mo.VirtualMachine
	if err := collector.RetrieveOne(f.ctx, f.vms[0].Reference(), []string{"name"}, &vm); err != nil {
		t.Fatalf("the original session must remain authenticated: %v", err)
	}
	var view mo.ContainerView
	err = collector.RetrieveOne(f.ctx, ref, []string{"view"}, &view)
	if !fault.Is(err, &types.ManagedObjectNotFound{}) {
		t.Fatalf("view after query: error=%v objects=%d DestroyView requests=%d; want ManagedObjectNotFound in the still-active session", err, len(view.View), destroys)
	}
}
