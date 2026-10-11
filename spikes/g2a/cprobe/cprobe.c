// cprobe: G2A-06 control. The app's loopback through the C boundary of libmedxfer, from a
// plain C program (no Flutter). Comparing it with oneproc (pure Go) and the app tells whether a
// gap comes from the c-shared library or from the Flutter process.
// Build: cc -O2 -o cprobe cprobe.c -ldl     Usage: cprobe <libmedxfer.so> <file> <out_dir>
// Prints the receiver's engine time and wall time in ms (events read in the poll model).
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef char *(*str_fn)(const char *);
typedef char *(*next_fn)(int);
typedef void (*free_fn)(char *);

static long field(const char *json, const char *key) {
    const char *p = strstr(json, key);
    return p ? atol(p + strlen(key)) : -1;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: cprobe <libmedxfer.so> <file> <out_dir>\n");
        return 2;
    }
    void *lib = dlopen(argv[1], RTLD_NOW);
    if (!lib) {
        fprintf(stderr, "%s\n", dlerror());
        return 1;
    }
    str_fn start = (str_fn)dlsym(lib, "medxfer_start");
    str_fn call = (str_fn)dlsym(lib, "medxfer_call");
    next_fn next = (next_fn)dlsym(lib, "medxfer_next_event");
    free_fn mfree = (free_fn)dlsym(lib, "medxfer_free");

    mfree(start("{}"));
    char *ms = call("{\"id\":\"m\",\"action\":\"memstats\"}");
    fprintf(stderr, "memstats %s
", ms);
    mfree(ms);
    char req[4096];
    snprintf(req, sizeof req, "{\"id\":\"s\",\"action\":\"send_file\",\"payload\":{\"path\":\"%s\",\"port\":19988}}", argv[2]);
    char *r = call(req);
    if (!strstr(r, "\"ok\":true")) {
        fprintf(stderr, "send_file: %s\n", r);
        return 1;
    }
    mfree(r);
    snprintf(req, sizeof req, "{\"id\":\"r\",\"action\":\"receive_file\",\"payload\":{\"addr\":\"127.0.0.1:19988\",\"out_dir\":\"%s\"}}", argv[3]);
    mfree(call(req));

    for (;;) { // poll model: no callback set, events wait in the core's queue
        char *e = next(1000);
        if (!e) continue;
        int done = strstr(e, "\"transfer_done\"") && strstr(e, "\"role\":\"recv\"");
        if (done) printf("%ld %ld\n", field(e, "\"engine_ms\":"), field(e, "\"wall_ms\":"));
        if (strstr(e, "\"transfer_error\"")) {
            fprintf(stderr, "%s\n", e);
            return 1;
        }
        mfree(e);
        if (done) return 0;
    }
}
