#ifndef RUNNER_HOTSPOT_H_
#define RUNNER_HOTSPOT_H_

#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <vector>

#include <winrt/Windows.Devices.WiFiDirect.h>

// The interface the group runs on: the app serves addresses there (lib/platform/dhcp_server.dart)
// and gives it to the core (DISC-10).
struct HotspotInterface {
  std::string name;
  uint32_t index = 0;
  std::string address;
  uint8_t prefix = 0;
  // Windows serves the addresses itself (its connection sharing, ICS, gives the group
  // 192.168.137.1): the phones are accepted and stay. Otherwise the group has a link-local
  // address, the app serves them, and Windows drops each phone after 2 minutes.
  bool windows_dhcp = false;
  // Without: the card that holds the sharing's address (a sharing to it, or one left behind),
  // and that address.
  std::string held_by;
  std::string sharing_address;
};

// The network this computer creates when none is available (DISC-12, WP-C.2 of the J6c plan):
// a Wi-Fi Direct group it owns, joined by the others as an ordinary Wi-Fi network ("legacy"
// settings), with a name and passphrase chosen by the app. No Internet connection to share, no
// administrator rights (probe WP-C.1, decision C4). Windows serves the addresses on it, through
// its connection sharing, unless that sharing is taken by another card (HotspotInterface). The
// driver picks the band: 2.4 GHz on the probe's PC, its card not starting 5 GHz on its own. The
// group lives as long as the publisher, so until Stop or the end of the app.
class WifiDirectHotspot {
 public:
  ~WifiDirectHotspot();

  // What Start gives once done: the error, if any, else the group's interface.
  using Started = std::function<void(std::optional<std::string> error, HotspotInterface group)>;

  // Starts the group and finds its interface, then calls done, on the window's thread: called
  // from it, it waits off it, so that the window keeps drawing (up to 7 s of waits, the window
  // frozen as long before; real test of 30 October 2026). A Stop meanwhile ends it.
  winrt::fire_and_forget Start(std::string ssid, std::string passphrase, Started done);
  void Stop();

  // Whether the group still runs: Windows stops it when the computer's Wi-Fi joins another
  // network, and the publisher still says Started (#219; Microsoft Q&A 2285169).
  bool Alive();

 private:
  winrt::fire_and_forget Accept(winrt::Windows::Devices::WiFiDirect::WiFiDirectConnectionRequest request);

  winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisher publisher_{nullptr};
  uint32_t group_index_ = 0;  // the group's adapter
  uint64_t generation_ = 0;   // each Stop: a Start still waiting gives up
  // An Android phone joining is a request Windows asks the app to accept; without an answer, it
  // drops the phone after 2 minutes (real tests of 28 and 29 October 2026, two phones).
  // Accepted, the phone is kept while its WiFiDirectDevice lives. Accepting needs Windows to
  // serve the addresses: otherwise it fails after 30 s, and the listener is let go.
  winrt::Windows::Devices::WiFiDirect::WiFiDirectConnectionListener listener_{nullptr};
  winrt::event_token requested_{};
  std::optional<winrt::apartment_context> ui_;
  std::vector<winrt::Windows::Devices::WiFiDirect::WiFiDirectDevice> accepted_;
};

#endif  // RUNNER_HOTSPOT_H_
