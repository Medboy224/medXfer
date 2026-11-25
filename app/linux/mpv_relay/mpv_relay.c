// medXfer's relay to the system's libmpv (plan J8, WP-8.2b, decision D8).
//
// media_kit_video's plugin links libmpv. Linked to it directly, the app needs one libmpv to
// start at all, and one of a given number: libmpv.so.1 on Ubuntu 22.04 and Zorin 17,
// libmpv.so.2 on Ubuntu 24.04 and Debian 12. The plugin is linked to this relay instead: it
// carries the seven functions the plugin calls, and passes each call to the libmpv of the
// system, found when a video first plays: the one media_kit already opened (libmpv.so,
// libmpv.so.2, libmpv.so.1, in the order media_kit tries them), else the first of them there.
// Without any, the app starts all the same, and the calls fail: media_kit says libmpv is missing
// before a video gets here. No code of mpv is in this file: its headers give the types only.

#include <dlfcn.h>
#include <pthread.h>
#include <stddef.h>

#include <mpv/client.h>
#include <mpv/render.h>

static const char *const kNames[] = {"libmpv.so", "libmpv.so.2", "libmpv.so.1"};

static void *library;
static pthread_once_t once = PTHREAD_ONCE_INIT;

static void find_library(void) {
  // The one media_kit opened first: the same instance of mpv as its player's.
  for (size_t i = 0; i < sizeof kNames / sizeof kNames[0] && library == NULL; i++) {
    library = dlopen(kNames[i], RTLD_NOW | RTLD_NOLOAD);
  }
  for (size_t i = 0; i < sizeof kNames / sizeof kNames[0] && library == NULL; i++) {
    library = dlopen(kNames[i], RTLD_NOW | RTLD_GLOBAL);
  }
}

static void *find(const char *name) {
  pthread_once(&once, find_library);
  return library == NULL ? NULL : dlsym(library, name);
}

int mpv_set_option_string(mpv_handle *ctx, const char *name, const char *data) {
  static int (*f)(mpv_handle *, const char *, const char *);
  if (f == NULL) f = (int (*)(mpv_handle *, const char *, const char *))find("mpv_set_option_string");
  return f == NULL ? MPV_ERROR_NOT_IMPLEMENTED : f(ctx, name, data);
}

int mpv_get_property(mpv_handle *ctx, const char *name, mpv_format format, void *data) {
  static int (*f)(mpv_handle *, const char *, mpv_format, void *);
  if (f == NULL) f = (int (*)(mpv_handle *, const char *, mpv_format, void *))find("mpv_get_property");
  return f == NULL ? MPV_ERROR_NOT_IMPLEMENTED : f(ctx, name, format, data);
}

void mpv_free_node_contents(mpv_node *node) {
  static void (*f)(mpv_node *);
  if (f == NULL) f = (void (*)(mpv_node *))find("mpv_free_node_contents");
  if (f != NULL) f(node);
}

int mpv_render_context_create(mpv_render_context **res, mpv_handle *mpv, mpv_render_param *params) {
  static int (*f)(mpv_render_context **, mpv_handle *, mpv_render_param *);
  if (f == NULL) f = (int (*)(mpv_render_context **, mpv_handle *, mpv_render_param *))find("mpv_render_context_create");
  return f == NULL ? MPV_ERROR_NOT_IMPLEMENTED : f(res, mpv, params);
}

void mpv_render_context_set_update_callback(mpv_render_context *ctx, mpv_render_update_fn callback, void *callback_ctx) {
  static void (*f)(mpv_render_context *, mpv_render_update_fn, void *);
  if (f == NULL) f = (void (*)(mpv_render_context *, mpv_render_update_fn, void *))find("mpv_render_context_set_update_callback");
  if (f != NULL) f(ctx, callback, callback_ctx);
}

int mpv_render_context_render(mpv_render_context *ctx, mpv_render_param *params) {
  static int (*f)(mpv_render_context *, mpv_render_param *);
  if (f == NULL) f = (int (*)(mpv_render_context *, mpv_render_param *))find("mpv_render_context_render");
  return f == NULL ? MPV_ERROR_NOT_IMPLEMENTED : f(ctx, params);
}

void mpv_render_context_free(mpv_render_context *ctx) {
  static void (*f)(mpv_render_context *);
  if (f == NULL) f = (void (*)(mpv_render_context *))find("mpv_render_context_free");
  if (f != NULL) f(ctx);
}
