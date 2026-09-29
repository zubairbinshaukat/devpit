//go:build shots

package shots

import (
	"context"
	"reflect"
	"time"

	updateui "github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
)

// fakeWorker stands in for the elevated helper process. It satisfies the
// update screen's private worker interface, which is only about its two
// exported method names.
type fakeWorker struct {
	// hold makes Exec wait until the run is stopped, as a long admin install
	// would.
	hold bool
}

// Exec pretends to run one command as administrator.
func (w *fakeWorker) Exec(ctx context.Context, _ []string, _ time.Duration, onLine func(stream, text string)) (int, error) {
	if w.hold {
		// Still starting, as an elevated winget is before its first line.
		<-ctx.Done()
		return -1, nil
	}
	onLine("stdout", "Successfully installed")
	return 0, nil
}

// Close ends the fake helper.
func (*fakeWorker) Close() error { return nil }

// launcher builds the update screen's option for starting the admin helper.
// The screen's launcher type names an interface it does not export, so a
// function of that type cannot be written outside the package; reflection
// builds one from the option's own parameter type. worker is what a
// successful launch returns, and err what a failed one does (a declined
// Windows prompt, for instance).
func launcher(worker *fakeWorker, err error) updateui.Option {
	opt := reflect.ValueOf(updateui.WithLaunchElevatedFunc)
	fnType := opt.Type().In(0)
	fn := reflect.MakeFunc(fnType, func([]reflect.Value) []reflect.Value {
		w := reflect.New(fnType.Out(0)).Elem()
		if worker != nil {
			w.Set(reflect.ValueOf(worker))
		}
		e := reflect.New(fnType.Out(1)).Elem()
		if err != nil {
			e.Set(reflect.ValueOf(err))
		}
		return []reflect.Value{w, e}
	})
	return opt.Call([]reflect.Value{fn})[0].Interface().(updateui.Option)
}
