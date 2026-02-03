package state

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation

#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>
#include <CoreFoundation/CoreFoundation.h>

// IOPMUserIsActive is a private API that returns true when the user is active
// (full wake) and false during dark wake (Power Nap) or when display is asleep.
// Declaration from IOPMLibPrivate.h (not in public SDK).
extern Boolean IOPMUserIsActive(void);

// Forward declarations for the Go callback
extern void goSleepCallback(int messageType);

static io_connect_t rootPort;
static IONotificationPortRef notifyPortRef;
static io_object_t notifierObject;

// Message types:
// 1 = sleep
// 2 = wake

static void sleepCallbackC(void *refCon, io_service_t service, natural_t messageType, void *messageArgument) {
	switch (messageType) {
	case kIOMessageCanSystemSleep:
		IOAllowPowerChange(rootPort, (long)messageArgument);
		break;
	case kIOMessageSystemWillSleep:
		goSleepCallback(1); // sleep
		IOAllowPowerChange(rootPort, (long)messageArgument);
		break;
	case kIOMessageSystemHasPoweredOn:
		goSleepCallback(2); // wake
		break;
	}
}

static int registerSleepCallbacks(void) {
	rootPort = IORegisterForSystemPower(NULL, &notifyPortRef, sleepCallbackC, &notifierObject);
	if (rootPort == 0) {
		return -1;
	}
	CFRunLoopAddSource(CFRunLoopGetCurrent(), IONotificationPortGetRunLoopSource(notifyPortRef), kCFRunLoopDefaultMode);
	return 0;
}

static void deregisterSleepCallbacks(void) {
	CFRunLoopRemoveSource(CFRunLoopGetCurrent(), IONotificationPortGetRunLoopSource(notifyPortRef), kCFRunLoopDefaultMode);
	IODeregisterForSystemPower(&notifierObject);
	IOServiceClose(rootPort);
	IONotificationPortDestroy(notifyPortRef);
}

static void runRunLoop(void) {
	CFRunLoopRun();
}

static void stopRunLoop(CFRunLoopRef rl) {
	CFRunLoopStop(rl);
}

// queryUserIsActive checks if the user is currently active (not in dark wake)
static int queryUserIsActive(void) {
	return IOPMUserIsActive() ? 1 : 0;
}
*/
import "C"

import (
	"context"
	"sync"
)

var (
	globalSleepMonitor *SleepMonitor
	globalSleepMu      sync.Mutex
)

//export goSleepCallback
func goSleepCallback(messageType C.int) {
	globalSleepMu.Lock()
	m := globalSleepMonitor
	globalSleepMu.Unlock()

	if m == nil {
		return
	}

	switch messageType {
	case 1:
		m.markSleep()
	case 2:
		m.markWake()
	}
}

// Start begins listening for system sleep/wake events using IOKit.
func (m *SleepMonitor) Start(ctx context.Context) {
	globalSleepMu.Lock()
	globalSleepMonitor = m
	globalSleepMu.Unlock()

	go func() {
		if ret := C.registerSleepCallbacks(); ret != 0 {
			m.logger.Error("Failed to register for system power notifications")
			return
		}

		// Get the run loop ref before entering it so we can stop it from another goroutine
		rl := C.CFRunLoopGetCurrent()

		go func() {
			<-ctx.Done()
			C.stopRunLoop(rl)
		}()

		C.runRunLoop()
		C.deregisterSleepCallbacks()

		globalSleepMu.Lock()
		globalSleepMonitor = nil
		globalSleepMu.Unlock()

		m.logger.Debug("Sleep monitor stopped")
	}()

	m.logger.Info("Sleep monitor started (IOKit)")
}

// isUserActive returns true if the user is currently active (not in dark wake).
// This is used to suppress probes during Power Nap.
func (m *SleepMonitor) isUserActive() bool {
	return C.queryUserIsActive() != 0
}
