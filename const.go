package kevent

const (
	KE_Event_None     = 0
	KE_Event_Readable = 1 << 0
	KE_Event_Writable = 1 << 1
	KE_Event_Barrier  = 1 << 2
)

const (
	KE_DeleteEventId   = -1
	KE_FileEvents      = 1 << 0
	KE_TimeEvents      = 1 << 1
	KE_AllEvents       = KE_FileEvents | KE_TimeEvents
	KE_DontWait        = 1 << 2
	KE_CallBeforeSleep = 1 << 3
	KE_CallAfterSleep  = 1 << 4
)
