package daemon

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestScansWithNothingChangedReuseOneViewState(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	runTurn(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.background.wait()
	for range 3 {
		d.publishCatalogPresence()
	}
	builds := d.catalogView.builds
	for range 3 {
		d.publishCatalogPresence()
	}
	if d.catalogView.builds != builds {
		t.Fatalf("three unchanged presence passes built the view state %d more times", d.catalogView.builds-builds)
	}
}

func TestATurnNoticeRecordedAloneReachesItsRowThroughTheCachedViewState(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	runTurn(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.background.wait()
	for range 3 {
		if row := viewRow(t, d, from.ID); row.TurnCompletion == nil || row.TurnCompletion.TurnID != "turn-1" {
			t.Fatalf("the first turn's completion %+v", row.TurnCompletion)
		}
	}
	builds := d.catalogView.builds
	viewRow(t, d, from.ID)
	if d.catalogView.builds != builds {
		t.Fatal("the view state was rebuilt with nothing changed, so the notice below would not test the cache")
	}
	if err := r.RecordCompletion(from.ID, &registry.TurnNotice{TurnID: "turn-2", Outcome: turnOutcomeFailed, At: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if row := viewRow(t, d, from.ID); row.TurnCompletion == nil || row.TurnCompletion.TurnID != "turn-2" || row.TurnCompletion.Outcome != turnOutcomeFailed {
		t.Fatalf("a notice recorded alone did not reach the row: %+v", row.TurnCompletion)
	}
}

func TestAReadStateListedByOnePassStaysOutOfTheNextCallersView(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	var passes sync.WaitGroup
	for pass := range 4 {
		passes.Go(func() {
			view, err := d.loadCatalogViewState()
			if err != nil {
				t.Error(err)
				return
			}
			listed := from
			listed.ReadState = &surface.ReadState{Revision: fmt.Sprintf("listed-only-%d", pass)}
			listedReadStates([]surface.Session{listed}, view)
		})
	}
	passes.Wait()
	next, err := d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	if read, ok := next.reads[from.ID]; ok {
		t.Fatalf("a read state listed but never stored reached the next caller: %+v", read)
	}
}

func TestEveryMapAndSliceInTheViewStateIsCopiedForEachCaller(t *testing.T) {
	var cached catalogViewState
	fillReferences(t, reflect.ValueOf(&cached).Elem(), "catalogViewState")
	copied := cached.clone()
	assertNoSharedReferences(t, reflect.ValueOf(cached), reflect.ValueOf(copied), "catalogViewState")
}

func settable(v reflect.Value) reflect.Value {
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

func fillReferences(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			fillReferences(t, settable(v.Field(i)), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		t.Fatalf("%s is a %s, which clone cannot copy; store it by value", path, v.Kind())
	}
}

func assertNoSharedReferences(t *testing.T, cached, copied reflect.Value, path string) {
	t.Helper()
	switch cached.Kind() {
	case reflect.Struct:
		for i := range cached.NumField() {
			assertNoSharedReferences(t, cached.Field(i), copied.Field(i), path+"."+cached.Type().Field(i).Name)
		}
	case reflect.Map, reflect.Slice:
		if copied.IsNil() || copied.UnsafePointer() == cached.UnsafePointer() {
			t.Errorf("%s is shared between the cached view state and a caller's copy", path)
		}
	}
}
