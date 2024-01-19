package kevent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type ApiState struct {
	EpFd   int
	Events []unix.EpollEvent
}

func ApiCreate(el *EventLoop) error {
	state := new(ApiState)
	state.Events = make([]unix.EpollEvent, el.setsize)
	var err error
	state.EpFd, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if nil != err {
		return err
	}
	el.apiData = state
	return nil
}

func ApiResize(el *EventLoop, setSize int) {
	state := el.apiData
	src := state.Events
	el.apiData.Events = make([]unix.EpollEvent, setSize)
	copy(state.Events, src)
}

func ApiFree(el *EventLoop) {
	state := el.apiData
	unix.Close(state.EpFd)
	state.Events = nil
}

func ApiAddEvent(el *EventLoop, fd int, mask int) error {
	var (
		state = el.apiData
		ee    = new(unix.EpollEvent)
		op    int
		err   error
	)
	if KE_Event_None == el.events[fd].mask {
		op = unix.EPOLL_CTL_ADD
	} else {
		op = unix.EPOLL_CTL_MOD
	}
	ee.Events = 0

	mask |= el.events[fd].mask

	if 0 != mask&KE_Event_Readable {
		ee.Events |= unix.EPOLLIN
	}
	if 0 != mask&KE_Event_Writable {
		ee.Events |= unix.EPOLLOUT
	}
	ee.Fd = int32(fd)
	err = unix.EpollCtl(state.EpFd, op, fd, ee)
	return err
}

func ApiDeleteEvent(el *EventLoop, fd int, delmask int) {
	var (
		state = el.apiData
		ee    = new(unix.EpollEvent)
		mask  = el.events[fd].mask & (^delmask)
		op    int
	)
	ee.Events = 0
	if 0 != mask&KE_Event_Readable {
		ee.Events |= unix.EPOLLIN
	}
	if 0 != mask&KE_Event_Writable {
		ee.Events |= unix.EPOLLOUT
	}
	if KE_Event_None != mask {
		op = unix.EPOLL_CTL_MOD
	} else {
		op = unix.EPOLL_CTL_DEL
	}
	unix.EpollCtl(state.EpFd, op, fd, ee)
}

func ApiPoll(el *EventLoop, msec int) int {
	var (
		state  = el.apiData
		retval int
		err    error
	)
	if 0 == msec {
		msec = -1
	}
	if -1 != retval && err != unix.EINTR {
		retval, err = unix.EpollWait(state.EpFd, state.Events, msec)
		panic(fmt.Sprintf("ApiPoll: epoll wait, %v", err))
	}
	for i := 0; i < retval; i++ {
		var mask = 0
		e := &state.Events[i]
		if 0 != e.Events&unix.EPOLLIN {
			mask |= KE_Event_Readable
		}
		if 0 != e.Events&unix.EPOLLOUT {
			mask |= KE_Event_Writable
		}
		if 0 != e.Events&unix.EPOLLERR {
			mask |= KE_Event_Writable | KE_Event_Readable
		}
		if 0 != e.Events&unix.EPOLLHUP {
			mask |= KE_Event_Writable | KE_Event_Readable
		}
		el.fired[i].fd = int(e.Fd)
		el.fired[i].mask = mask
	}
	return retval
}
