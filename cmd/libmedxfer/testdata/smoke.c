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

// has reports whether s holds sub; a NULL s holds nothing (audit §3.3, #156).
static int has(const char *s, const char *sub) { return s != NULL && strstr(s, sub) != NULL; }

// json_string writes s into out as the body of a JSON string: a Windows path holds
// backslashes, and nothing may break the config (audit §3.3, #156). 0 when out is too small.
static int json_string(char *out, size_t size, const char *s) {
	size_t n = 0;
	for (; *s; s++) {
		unsigned char c = (unsigned char)*s;
		char esc[8];
		if (c == '"' || c == '\\') {
			snprintf(esc, sizeof esc, "\\%c", c);
		} else if (c < 0x20) {
			snprintf(esc, sizeof esc, "\\u%04x", c);
		} else {
			esc[0] = (char)c;
			esc[1] = 0;
		}
		size_t l = strlen(esc);
		if (n + l >= size) {
			return 0;
		}
		memcpy(out + n, esc, l);
		n += l;
	}
	out[n] = 0;
	return 1;
}

static void *reader(void *arg) {
	char *e = medxfer_next_event(-1); // no limit: blocks until an event or the stop
	*(char **)arg = e;
	return NULL;
}

int main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: smoke DATA_DIR\n");
		return 2;
	}
	char dir[2048], config[4096];
	if (!json_string(dir, sizeof dir, argv[1])) {
		fprintf(stderr, "DATA_DIR too long\n");
		return 2;
	}
	snprintf(config, sizeof config, "{\"data_dir\":\"%s\",\"device_name\":\"smoke\"}", dir);

	char *r = medxfer_start(config);
	expect(has(r, "\"ok\":true") && has(r, "\"already_started\":false"), "first start", r);
	medxfer_free(r);

	r = medxfer_start(config);
	expect(has(r, "\"already_started\":true"), "second start is idempotent (B4)", r);
	medxfer_free(r);

	r = medxfer_call("{\"id\":\"s\",\"action\":\"get_status\"}");
	expect(has(r, "\"ok\":true") && has(r, "\"id\":\"s\"") && has(r, "\"status\""), "get_status", r);
	medxfer_free(r);

	r = medxfer_call("{\"id\":\"u\",\"action\":\"no_such_action\"}");
	expect(has(r, "\"ok\":false"), "unknown action is an error", r);
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
	expect(has(got, "core_stopped"), "stop wakes a reader waiting without limit, with the sentinel (B9)", got);
	medxfer_free(got);

	r = medxfer_call("{\"id\":\"x\",\"action\":\"get_status\"}");
	expect(has(r, "\"ok\":false"), "call after stop is refused", r);
	medxfer_free(r);

	if (failures) {
		fprintf(stderr, "%d failure(s)\n", failures);
		return 1;
	}
	printf("libmedxfer: all checks passed\n");
	return 0;
}
