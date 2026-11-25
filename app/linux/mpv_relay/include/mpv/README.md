These are libmpv's API headers, `client.h`, `render.h` and `render_gl.h`, unchanged, from mpv
v0.34.1 (`libmpv/` in https://github.com/mpv-player/mpv, tag `v0.34.1`). They are under the ISC
licence written at the top of each file, which is not mpv's own licence.

They contain declarations only. medXfer's relay (`../../mpv_relay.c`) and media_kit_video's Linux
plugin are built against them, so neither `libmpv-dev` nor libmpv itself is needed to build the
app (plan J8, WP-8.2b, decision D8).

They come from mpv 0.34.1, the version of `libmpv1` on Ubuntu 22.04 and Zorin 17, the oldest
libmpv medXfer supports. What they declare is also in later versions (`libmpv2`).
