#ifndef RUNNER_HOTSPOT_H_
#define RUNNER_HOTSPOT_H_

#include <cstdint>
#include <optional>
#include <string>

#include <winrt/Windows.Devices.WiFiDirect.h>

// The interface the group runs on: the app serves addresses there (lib/platform/dhcp_server.dart)
// and gives it to the core (DISC-10).
struct HotspotInterface {
  std::string name;
  uint32_t index = 0;
  std::string address;
  uint8_t prefix = 0;
};

// The network this computer creates when none is available (DISC-12, WP-C.2 of the J6c plan):
// a Wi-Fi Direct group it owns, joined by the others as an ordinary Wi-Fi network ("legacy"
// settings), with a name and passphrase chosen by the app. No Internet connection to share, no
// administrator rights (probe WP-C.1, decision C4). Windows serves no addresses on it: the app
// does. The driver picks the band: 2.4 GHz on the probe's PC, its card not starting 5 GHz on its
// own. The group lives as long as the publisher, so until Stop or the end of the app.
class WifiDirectHotspot {
 public:
  ~WifiDirectHotspot();

  // Starts the group and finds its interface; the error, if either fails.
  std::optional<std::string> Start(const std::string& ssid, const std::string& passphrase,
                                   HotspotInterface* out);
  void Stop();

 private:
  winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisher publisher_{nullptr};
};

#endif  // RUNNER_HOTSPOT_H_
