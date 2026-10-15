package vsphere_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/govmomi/vim25/xml"

	"github.com/futuretea/vsphere-mcp-server/pkg/core/config"
	vspheretools "github.com/futuretea/vsphere-mcp-server/pkg/toolset/vsphere"
	"github.com/futuretea/vsphere-mcp-server/pkg/vsphere"
)

func TestResourceFaultPropagatesDeniedProperty(t *testing.T) {
	for _, deniedProperty := range []string{"name", "summary"} {
		t.Run(deniedProperty, func(t *testing.T) {
			f := newQueryFixture(t, 1)
			ref := f.vms[0].Reference()
			var injected atomic.Bool
			frontend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, err := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if err != nil {
					t.Errorf("read synthetic SOAP request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(request))
				recorder := httptest.NewRecorder()
				f.model.Service.ServeMux.ServeHTTP(recorder, r)
				response := recorder.Body.Bytes()
				var requestBody methods.RetrievePropertiesExBody
				if err := decodeResourceSOAP(request, &requestBody); err != nil {
					t.Errorf("decode synthetic SOAP request: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if isResourceDetailRequest(requestBody.Req, ref) {
					response, err = denyResourceProperty(response, ref, deniedProperty)
					if err != nil {
						t.Errorf("encode synthetic denied-property response: %v", err)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					injected.Store(true)
				}
				for key, values := range recorder.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(response)
			}))
			t.Cleanup(frontend.Close)
			endpoint := *f.model.Service.Listen
			endpoint.Host = strings.TrimPrefix(frontend.URL, "https://")
			service := vsphere.NewService(config.VSphereConfig{Endpoint: endpoint.String(), Insecure: true})
			t.Cleanup(func() { _ = service.Close(context.Background()) })
			f.tools = vspheretools.NewToolset(service, true, true).GetTools()
			result, err := f.tool(t, "vsphere_get_resource").Handler(f.ctx, map[string]any{"entity_type": ref.Type, "entity_id": ref.Value})
			if !injected.Load() {
				t.Fatal("fixture did not inject MissingSet into the selected resource response")
			}
			if !fault.Is(err, &types.NoPermission{}) {
				t.Fatalf("denied %s: error = %v, want NoPermission rather than successful partial resource; response bytes = %d", deniedProperty, err, len(result))
			}
		})
	}
}

func decodeResourceSOAP(data []byte, body *methods.RetrievePropertiesExBody) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.TypeFunc = types.TypeFunc()
	return decoder.Decode(&soap.Envelope{Body: body})
}

func isResourceDetailRequest(request *types.RetrievePropertiesEx, ref types.ManagedObjectReference) bool {
	if request == nil {
		return false
	}
	for _, filter := range request.SpecSet {
		for _, object := range filter.ObjectSet {
			if object.Obj != ref {
				continue
			}
			for _, properties := range filter.PropSet {
				if slices.Contains(properties.PathSet, "name") && slices.Contains(properties.PathSet, "summary") {
					return true
				}
			}
		}
	}
	return false
}

func denyResourceProperty(data []byte, ref types.ManagedObjectReference, property string) ([]byte, error) {
	var body methods.RetrievePropertiesExBody
	if err := decodeResourceSOAP(data, &body); err != nil {
		return nil, err
	}
	for i := range body.Res.Returnval.Objects {
		object := &body.Res.Returnval.Objects[i]
		if object.Obj != ref {
			continue
		}
		object.PropSet = slices.DeleteFunc(object.PropSet, func(value types.DynamicProperty) bool { return value.Name == property })
		object.MissingSet = append(object.MissingSet, types.MissingProperty{Path: property, Fault: types.LocalizedMethodFault{
			Fault: &types.NoPermission{Object: &ref, PrivilegeId: "System.View"}, LocalizedMessage: "synthetic property access denied",
		}})
	}
	return xml.Marshal(soap.Envelope{Body: &body})
}
