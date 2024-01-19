package kevent

import (
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type FileProc func(el *EventLoop, fd int, clientData any, mask int)
type TimeProc func(el *EventLoop, id int64, clientData any) int64
type EventFinalizerProc func(el *EventLoop, clientData any)
type NowProc func() time.Time
type BeforeSleep func(el *EventLoop)

type fileEvent struct {
	mask       int
	rFileProc  FileProc
	wFileProc  FileProc
	clientData any
}

type firedEvent struct {
	fd   int
	mask int
}

type TimeEvent struct {
	id            int64
	when          int64
	timeProc      TimeProc
	finalizerProc EventFinalizerProc
	clientData    any
	prev          *TimeEvent
	next          *TimeEvent
	refcount      int
}

type EventLoop struct {
	setsize         int
	maxfd           int
	apiData         *ApiState
	events          []fileEvent
	fired           []firedEvent
	timeEventHead   *TimeEvent
	timeEventNextId int64
	nowProc         NowProc
	flags           int
	beforeSleep     BeforeSleep
	afterSleep      BeforeSleep
}

func CreateEventLoop(setsize int) *EventLoop {
	var (
		el  = new(EventLoop)
		err error
	)
	el.events = make([]fileEvent, setsize)
	el.fired = make([]firedEvent, setsize)
	el.setsize = setsize
	err = ApiCreate(el)
	if nil != err {
		return nil
	}
	for i := range el.events {
		el.events[i].mask = KE_Event_None
	}
	return el
}

func GetSetSize(el *EventLoop) int {
	return el.setsize
}

func ResizeSetSize(el *EventLoop, setsize int) bool {
	if setsize == el.setsize {
		return true
	}
	if el.maxfd >= setsize {
		return false
	}
	ApiResize(el, setsize)
	events := el.events
	fired := el.fired
	el.events = make([]fileEvent, setsize)
	el.fired = make([]firedEvent, setsize)
	copy(el.events, events)
	copy(el.fired, fired)

	for i := el.maxfd + 1; i < setsize; i++ {
		el.events[i].mask = KE_Event_None
	}
	return true
}

func DeleteEventLoop(el *EventLoop) {
	ApiFree(el)
	el.events = nil
	el.fired = nil
}

func CreateFileEvent(el *EventLoop, fd int, mask int, proc FileProc, clientData any) bool {
	if fd >= el.setsize {
		return false
	}
	var (
		fe  = &el.events[fd]
		err error
	)
	err = ApiAddEvent(el, fd, mask)
	if nil != err {
		return false
	}
	fe.mask |= mask
	if 0 != mask&KE_Event_Readable {
		fe.rFileProc = proc
	}
	if 0 != mask&KE_Event_Writable {
		fe.wFileProc = proc
	}
	fe.clientData = clientData
	if fd > el.maxfd {
		el.maxfd = fd
	}
	return true
}

func DeleteFileEvent(el *EventLoop, fd int, mask int) {
	if fd >= el.setsize {
		return
	}
	var (
		fe = &el.events[fd]
	)
	if fe.mask == KE_Event_None {
		return
	}
	if 0 != fe.mask&KE_Event_Writable {
		mask |= KE_Event_Barrier
	}
	ApiDeleteEvent(el, fd, mask)
	fe.mask &= (^mask)
	if fd == el.maxfd && fe.mask == KE_Event_None {
		var j int
		for j = el.maxfd - 1; j >= 0; j-- {
			if el.events[j].mask != KE_Event_None {
				break
			}
		}
		el.maxfd = j
	}
}

func GetFileClientData(el *EventLoop, fd int) any {
	if fd >= el.setsize {
		return nil
	}
	fe := &el.events[fd]
	if fe.mask == KE_Event_None {
		return nil
	}
	return fe.clientData
}

func CreateTimeEvent(el *EventLoop, duration time.Duration, proc TimeProc, clientData any, finalizerProc EventFinalizerProc) int64 {
	el.timeEventNextId++
	var (
		id = el.timeEventNextId
		te = new(TimeEvent)
	)
	te.id = id
	te.when = el.nowProc().Add(duration).UnixMilli()
	te.timeProc = proc
	te.finalizerProc = finalizerProc
	te.prev = nil
	te.next = el.timeEventHead
	te.refcount = 0
	if nil != te.next {
		te.next.prev = te
	}
	el.timeEventHead = te
	return id
}

func DeleteTimeEvent(el *EventLoop, id int64) bool {
	te := el.timeEventHead
	for nil != te {
		if te.id == id {
			te.id = KE_DeleteEventId
			return true
		}
	}
	return false
}

func UntilEarliestTimer(el *EventLoop) int64 {
	var (
		te      = el.timeEventHead
		earlist *TimeEvent
	)
	if nil == te {
		return -1
	}
	for nil != te {
		if nil == earlist || te.when < earlist.when {
			earlist = te
		}
		te = te.next
	}
	now := el.nowProc().UnixMilli()
	diff := earlist.when - now
	if diff < 0 {
		return 0
	}
	return diff
}

func ProcessTimeEvents(el *EventLoop) int {
	var (
		processed = 0
		te        = el.timeEventHead
		maxId     = el.timeEventNextId - 1
		now       = el.nowProc().UnixMilli()
	)

	for nil != te {
		var id int64
		if te.id == KE_DeleteEventId {
			next := te.next
			if 1 == te.refcount {
				te = next
				continue
			}
			if nil != te.prev {
				te.prev.next = te.next
			} else {
				el.timeEventHead = te.next
			}
			if nil != te.next {
				te.next.prev = te.prev
			}
			if nil != te.finalizerProc {
				te.finalizerProc(el, te.clientData)
				now = el.nowProc().UnixMilli()
			}
			te = next
			continue
		}
		if te.id > maxId {
			te = te.next
			continue
		}
		if te.when <= now {
			var retval int64
			id = te.id
			te.refcount++
			retval = te.timeProc(el, id, te.clientData)
			te.refcount--
			processed++
			now = el.nowProc().UnixMilli()
			if retval != -1 {
				te.when = now + retval
			} else {
				te.id = KE_DeleteEventId
			}
		}
		te = te.next
	}
	return processed
}

func ProcessEvents(el *EventLoop, flags int) int {
	var (
		processed int
		numevents int
	)
	if 0 == flags&KE_AllEvents {
		return 0
	}
	if -1 == el.maxfd || 0 != flags&KE_TimeEvents && 0 == flags&KE_DontWait {
		var (
			untilTimer int64 = -1
		)
		if 0 != flags&KE_TimeEvents && 0 == flags&KE_DontWait {
			untilTimer = UntilEarliestTimer(el)
		}
		if untilTimer < 0 {
			if 0 != flags&KE_DontWait {
				untilTimer = 0
			} else {
				untilTimer = -1
			}
		}
		if 0 != el.flags&KE_DontWait {
			untilTimer = 0
		}
		if nil != el.beforeSleep && 0 != flags&KE_CallBeforeSleep {
			el.beforeSleep(el)
		}

		numevents = ApiPoll(el, int(untilTimer))

		if nil != el.afterSleep && 0 != flags&KE_CallAfterSleep {
			el.afterSleep(el)
		}
		for j := 0; j < numevents; j++ {
			fd := el.fired[j].fd
			fe := &el.events[fd]
			mask := el.fired[j].mask
			fired := 0
			invert := 0 != fe.mask&KE_Event_Barrier
			if !invert && 0 != fe.mask&mask&KE_Event_Readable {
				fe.rFileProc(el, fd, fe.clientData, mask)
				fired++
			}
			if 0 != fe.mask&mask&KE_Event_Writable {

				// reflect.ValueOf().Pointer()
				if 0 == fired || unsafe.Pointer(&fe.wFileProc) != unsafe.Pointer(&fe.rFileProc) {
					fe.wFileProc(el, fd, fe.clientData, mask)
					fired++
				}
			}

			if invert {
				fe = &el.events[fd]
				if 0 != fe.mask&mask&KE_Event_Readable &&
					(0 == fired || unsafe.Pointer(&fe.wFileProc) != unsafe.Pointer(&fe.rFileProc)) {
					fe.rFileProc(el, fd, fe.clientData, mask)
					fired++
				}
			}
			processed++
		}
	}
	if 0 != flags&KE_TimeEvents {
		processed += ProcessTimeEvents(el)
	}
	return processed
}

func Wait(fd int, mask int, milli int64) int {
	var (
		pfd     = new(unix.PollFd)
		retmask int
		retval  int
	)
	pfd.Fd = int32(fd)

	if 0 != mask&KE_Event_Readable {
		pfd.Events |= unix.POLLIN
	}
	if 0 != mask&KE_Event_Writable {
		pfd.Events |= unix.POLLOUT
	}

	retval, _ = unix.Poll([]unix.PollFd{*pfd}, int(milli))
	if 1 == retval {
		if 0 != pfd.Events&unix.POLLIN {
			retmask |= KE_Event_Readable
		}
		if 0 != pfd.Events&unix.POLLOUT {
			retmask |= KE_Event_Writable
		}
		if 0 != pfd.Events&unix.POLLERR {
			retmask |= KE_Event_Writable
		}
		if 0 != pfd.Events&unix.POLLHUP {
			retmask |= KE_Event_Writable
		}
		return retmask
	} else {
		return retval
	}
}

func Main(el *EventLoop) {
	for {
		ProcessEvents(el, KE_AllEvents|KE_CallBeforeSleep|KE_CallAfterSleep)
	}
}
