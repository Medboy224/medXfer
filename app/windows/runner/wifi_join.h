#ifndef RUNNER_WIFI_JOIN_H_
#define RUNNER_WIFI_JOIN_H_

#include <cstdint>
#include <functional>
#include <guiddef.h>
#include <optional>
#include <string>

#include <winrt/Windows.Foundation.h>

// The network another device created, joined by this computer itself (WP-D.3 of the J6d plan),
// as Android's WifiJoin.kt does: through Windows' Wi-Fi API (wlanapi), with a profile of this
// user's, no administrator rights. The profile is the app's for the time of the session: leaving
// deletes it, and the computer goes back to the network it was on.
struct JoinedNetwork {
  std::string name;  // the interface's
  uint32_t index = 0;
  std::string address;
  uint8_t prefix = 0;
  uint32_t frequency = 0;  // MHz
};

// The bands this computer creates a network on and joins one on (bit 0: 2.4 GHz, bit 1: 5 GHz),
// as the core's ble_server and ble_client take them. Its own network is at 2.4 GHz (probe WP-C.1);
// with Wi-Fi off, none is created or joined.
struct WifiBandMask {
  int create = 0;
  int join = 0;
};
WifiBandMask OwnWifiBands();

class WifiJoin {
 public:
  ~WifiJoin();

  // Joins [ssid]; [security] as the QR code gives it (1: WPA2, 2: WPA3 transition, 3: WPA3);
  // [band] '5' when the network is at 5 GHz, '2' at 2.4, 0 unknown. Errors: wifi_off, no_wifi,
  // no_band, join_failed, no_address. done is called on the calling thread, the window's.
  winrt::fire_and_forget Join(std::string ssid, std::string passphrase, int security, char band,
                              std::function<void(std::optional<std::string> error, JoinedNetwork joined)> done);
  void Leave();

 private:
  std::wstring profile_;   // the app's profile, while joined
  std::wstring previous_;  // the profile of the network the computer was on
  GUID card_{};
};

#endif  // RUNNER_WIFI_JOIN_H_
