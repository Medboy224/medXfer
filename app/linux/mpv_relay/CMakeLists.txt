# medXfer's relay to the system's libmpv (mpv_relay.c; plan J8, WP-8.2b, decision D8).
# Built with mpv's API headers kept here (include/, ISC): neither libmpv-dev nor libmpv is
# needed to build, nor libmpv to start.
enable_language(C)

set(MEDXFER_MPV_HEADERS "${CMAKE_CURRENT_SOURCE_DIR}/include")

add_library(medxfer_mpv SHARED mpv_relay.c)
target_include_directories(medxfer_mpv PRIVATE "${MEDXFER_MPV_HEADERS}")
target_link_libraries(medxfer_mpv PRIVATE ${CMAKE_DL_LIBS} pthread)
set_target_properties(medxfer_mpv PROPERTIES LIBRARY_OUTPUT_DIRECTORY "${CMAKE_CURRENT_BINARY_DIR}/lib")

# media_kit_video's plugin asks pkg-config for "mpv": it finds this one first, naming the relay
# where it would name libmpv, and these headers.
file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/pkgconfig/mpv.pc"
  "Name: mpv\n"
  "Description: medXfer's relay to the system's libmpv\n"
  "Version: 0.34.1\n"
  "Libs: -L${CMAKE_CURRENT_BINARY_DIR}/lib -lmedxfer_mpv\n"
  "Cflags: -I${MEDXFER_MPV_HEADERS}\n")
set(ENV{PKG_CONFIG_PATH} "${CMAKE_CURRENT_BINARY_DIR}/pkgconfig:$ENV{PKG_CONFIG_PATH}")
# A configure before this one may have kept the system's libmpv for "mpv": asked again.
unset(__pkg_config_checked_mpv CACHE)
