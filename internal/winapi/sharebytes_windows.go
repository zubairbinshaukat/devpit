//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The PDH calls, from pdh.dll. Docs:
// https://learn.microsoft.com/en-us/windows/win32/api/pdh/
var (
	pdhDLL                     = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQueryW          = pdhDLL.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW  = pdhDLL.NewProc("PdhAddEnglishCounterW")
	procPdhRemoveCounter       = pdhDLL.NewProc("PdhRemoveCounter")
	procPdhCollectQueryData    = pdhDLL.NewProc("PdhCollectQueryData")
	procPdhGetRawCounterArrayW = pdhDLL.NewProc("PdhGetRawCounterArrayW")
	procPdhCloseQuery          = pdhDLL.NewProc("PdhCloseQuery")
)

// PDH status codes this file reads (pdhmsg.h).
const (
	pdhCstatusValidData  = 0x00000000
	pdhCstatusNewData    = 0x00000001
	pdhCstatusNoInstance = 0x800007D1
	pdhMoreData          = 0x800007D2
	pdhNoData            = 0x800007D5
	pdhCstatusNoObject   = 0xC0000BB8
	pdhCstatusNoCounter  = 0xC0000BB9
)

// pdhStatus is a PDH_STATUS that is not success.
type pdhStatus uint32

func (s pdhStatus) Error() string { return fmt.Sprintf("pdh: status 0x%08X", uint32(s)) }

// missingCounter reports whether err says the counter set or the counter is
// not installed on this PC.
func missingCounter(err error) bool {
	var s pdhStatus
	return errors.As(err, &s) && (s == pdhCstatusNoObject || s == pdhCstatusNoCounter)
}

// pdhRawCounter is PDH_RAW_COUNTER. On amd64 and arm64, the only targets,
// Go lays it out as C does: FirstValue at offset 16, 40 bytes in all.
type pdhRawCounter struct {
	CStatus     uint32
	TimeStamp   windows.Filetime
	FirstValue  int64
	SecondValue int64
	MultiCount  uint32
}

// pdhRawCounterItem is PDH_RAW_COUNTER_ITEM_W.
type pdhRawCounterItem struct {
	Name     *uint16
	RawValue pdhRawCounter
}

// ShareBytes is a per-share SMB byte counter, read from Windows' performance
// counters through PDH. It is safe for concurrent use.
type ShareBytes struct {
	mu      sync.Mutex
	query   uintptr
	counter uintptr
	path    *uint16
	match   func(instance string) bool
	tally   byteTally
}

// pdhCall calls a PDH function and turns its PDH_STATUS into an error. Like
// LazyProc.Call, its arguments may be pointers converted to uintptr in the
// call itself; the directive keeps what they point to alive and in place.
//
//go:uintptrescapes
func pdhCall(p *windows.LazyProc, args ...uintptr) error {
	r, _, _ := p.Call(args...)
	if r != 0 {
		return pdhStatus(uint32(r)) //nolint:gosec // PDH_STATUS is a 32-bit LONG in a register
	}
	return nil
}

// openShareBytes opens a query over the English counter path and checks the
// counter exists. The share's instance need not exist yet: [ShareBytes.Read]
// looks for it every time.
func openShareBytes(path string, match func(string) bool) (*ShareBytes, error) {
	if err := pdhDLL.Load(); err != nil {
		return nil, fmt.Errorf("loading pdh.dll: %w", err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	s := &ShareBytes{path: p, match: match}
	if err := pdhCall(procPdhOpenQueryW, 0, 0, uintptr(unsafe.Pointer(&s.query))); err != nil {
		return nil, fmt.Errorf("PdhOpenQuery: %w", err)
	}
	if err := s.addCounter(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// addCounter adds the counter path to the query, by its English name.
func (s *ShareBytes) addCounter() error {
	if err := pdhCall(procPdhAddEnglishCounterW, s.query, uintptr(unsafe.Pointer(s.path)), 0,
		uintptr(unsafe.Pointer(&s.counter))); err != nil {
		s.counter = 0
		return fmt.Errorf("PdhAddEnglishCounter %s: %w", windows.UTF16PtrToString(s.path), err)
	}
	return nil
}

// Read returns the bytes counted for the share so far. The count only goes
// up: a session that dropped and came back starts a new instance at zero,
// and its bytes are added to what was counted before. It returns
// [ErrNoShareInstance] while the share has no instance.
func (s *ShareBytes) Read() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query == 0 {
		return 0, errors.New("winapi: the share counter is closed")
	}
	raw, err := s.find()
	if errors.Is(err, ErrNoShareInstance) {
		// A wildcard counter may hold the instance list it saw when it was
		// added; add it again so an instance that appeared since is seen.
		if s.counter != 0 {
			_ = pdhCall(procPdhRemoveCounter, s.counter)
			s.counter = 0
		}
		if aerr := s.addCounter(); aerr != nil {
			return 0, aerr
		}
		raw, err = s.find()
	}
	if err != nil {
		return 0, err
	}
	return s.tally.add(raw), nil
}

// find collects once and returns the share's raw count.
func (s *ShareBytes) find() (uint64, error) {
	items, buf, err := s.collect()
	defer runtime.KeepAlive(buf)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if it.Name == nil || !s.match(windows.UTF16PtrToString(it.Name)) {
			continue
		}
		st := it.RawValue.CStatus
		if st != pdhCstatusValidData && st != pdhCstatusNewData {
			return 0, fmt.Errorf("reading the share counter: %w", pdhStatus(st))
		}
		if it.RawValue.FirstValue < 0 {
			return 0, errors.New("winapi: the share counter went negative")
		}
		return uint64(it.RawValue.FirstValue), nil
	}
	return 0, ErrNoShareInstance
}

// collect samples the query and returns every instance's raw value. The
// names point into buf, which the caller keeps alive while it reads them.
func (s *ShareBytes) collect() ([]pdhRawCounterItem, []uint64, error) {
	if err := pdhCall(procPdhCollectQueryData, s.query); err != nil {
		var st pdhStatus
		if errors.As(err, &st) && st == pdhNoData {
			return nil, nil, ErrNoShareInstance
		}
		return nil, nil, fmt.Errorf("PdhCollectQueryData: %w", err)
	}
	for range 4 { // instances can appear between the size call and the read
		var size, count uint32
		err := pdhCall(procPdhGetRawCounterArrayW, s.counter, uintptr(unsafe.Pointer(&size)),
			uintptr(unsafe.Pointer(&count)), 0)
		var st pdhStatus
		switch {
		case err == nil:
			return nil, nil, ErrNoShareInstance // no instances at all
		case errors.As(err, &st) && (st == pdhNoData || st == pdhCstatusNoInstance):
			return nil, nil, ErrNoShareInstance
		case !errors.As(err, &st) || st != pdhMoreData:
			return nil, nil, fmt.Errorf("PdhGetRawCounterArray: %w", err)
		}
		// A []uint64 keeps the buffer 8-byte aligned for the structs in it.
		buf := make([]uint64, (size+7)/8)
		if len(buf) == 0 {
			return nil, nil, ErrNoShareInstance
		}
		err = pdhCall(procPdhGetRawCounterArrayW, s.counter, uintptr(unsafe.Pointer(&size)),
			uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
		if errors.As(err, &st) && st == pdhMoreData {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("PdhGetRawCounterArray: %w", err)
		}
		if uintptr(count)*unsafe.Sizeof(pdhRawCounterItem{}) > uintptr(len(buf))*8 {
			return nil, nil, errors.New("winapi: PDH returned more items than its buffer holds")
		}
		return unsafe.Slice((*pdhRawCounterItem)(unsafe.Pointer(&buf[0])), count), buf, nil
	}
	return nil, nil, errors.New("winapi: the SMB counter instances kept changing")
}

// instances lists the instance names the counter has right now, for tests.
func (s *ShareBytes) instances() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, buf, err := s.collect()
	defer runtime.KeepAlive(buf)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Name != nil {
			out = append(out, windows.UTF16PtrToString(it.Name))
		}
	}
	return out, nil
}

// Close releases the query. Reading after Close fails; closing twice is
// harmless.
func (s *ShareBytes) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query != 0 {
		_ = pdhCall(procPdhCloseQuery, s.query)
		s.query, s.counter = 0, 0
	}
}
