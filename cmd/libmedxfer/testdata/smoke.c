// smoke.c calls the five functions of libmedxfer through the real C boundary (bible §10.4,
// WP-6.4 of the J6a plan): B1 every string freed by medxfer_free, B3 JSON, B4 idempotent
// start, B9 stop wakes a blocked reader with the sentinel. Built and run by
// scripts/build-lib.sh --smoke (Linux, CI).
//
// Usage: smoke DATA_DIR
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// The header cgo writes next to the library: libmedxfer.h, or medxfer.h for medxfer.dll.
#ifdef MEDXFER_WIN
#include "medxfer.h"
#else
#include "libmedxfer.h"
#endif

static int failures = 0;

static void expect(int ok, const char *what, const char *got) {
	if (!ok) {
		fprintf(stderr, "FAIL %s: %s\n", what, got ? got : "(null)");
		failures++;
	} else {
		printf("ok   %s\n", what);
	}
}

static void *reader(void *arg) {
	char *e = medxfer_next_event(60000); // blocks until an event or the stop
	*(char **)arg = e;
	return NULL;
}

int main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: smoke DATA_DIR\n");
		return 2;
	}
	char config[4096];
	snprintf(config, sizeof config, "{\"data_dir\":\"%s\",\"device_name\":\"smoke\"}", argv[1]);

	char *r = medxfer_start(config);
	expect(strstr(r, "\"ok\":true") && strstr(r, "\"already_started\":false"), "first start", r);
	medxfer_free(r);

	r = medxfer_start(config);
	expect(strstr(r, "\"already_started\":true") != NULL, "second start is idempotent (B4)", r);
	medxfer_free(r);

	r = medxfer_call("{\"id\":\"s\",\"action\":\"get_status\"}");
	expect(strstr(r, "\"ok\":true") && strstr(r, "\"id\":\"s\"") && strstr(r, "\"status\""), "get_status", r);
	medxfer_free(r);

	r = medxfer_call("{\"id\":\"u\",\"action\":\"no_such_action\"}");
	expect(strstr(r, "\"ok\":false") != NULL, "unknown action is an error", r);
	medxfer_free(r);

	char *e;
	while ((e = medxfer_next_event(0)) != NULL) { // empty what the start queued
		medxfer_free(e);
	}
	e = medxfer_next_event(50);
	expect(e == NULL, "no event: NULL after the timeout", e);

	char *got = NULL;
	pthread_t t;
	pthread_create(&t, NULL, reader, &got);
	usleep(100 * 1000);
	medxfer_stop();
	pthread_join(t, NULL);
	expect(got && strstr(got, "core_stopped"), "stop wakes the reader with the sentinel (B9)", got);
	medxfer_free(got);

	r = medxfer_call("{\"id\":\"x\",\"action\":\"get_status\"}");
	expect(strstr(r, "\"ok\":false") != NULL, "call after stop is refused", r);
	medxfer_free(r);

	if (failures) {
		fprintf(stderr, "%d failure(s)\n", failures);
		return 1;
	}
	printf("libmedxfer: all checks passed\n");
	return 0;
}
