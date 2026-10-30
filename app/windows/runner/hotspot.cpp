#include "hotspot.h"

// winsock2 before windows.h: iphlpapi needs it.
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <iphlpapi.h>

#include <chrono>
#include <cstdio>
#include <vector>

#include <winrt/Windows.Devices.Enumeration.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Security.Credentials.h>

using winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisher;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisherStatus;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectConnectionListener;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectConnectionRequest;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectConnectionRequestedEventArgs;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectDevice;
using winrt::Windows::Security::Credentials::PasswordCredential;

namespace {

// Calls f(adapter, address, prefix) for each IPv4 address of each adapter.
template <typename F>
void ForEachIPv4(F f) {
  ULONG size = 16 * 1024;
  std::vector<unsigned char> buffer(size);
  auto* list = reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buffer.data());
  ULONG flags = GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER;
  ULONG rc = GetAdaptersAddresses(AF_INET, flags, nullptr, list, &size);
  if (rc == ERROR_BUFFER_OVERFLOW) {
    buffer.resize(size);
    list = reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buffer.data());
    rc = GetAdaptersAddresses(AF_INET, flags, nullptr, list, &size);
  }
  if (rc != NO_ERROR) return;
  for (auto* a = list; a; a = a->Next) {
    for (auto* u = a->FirstUnicastAddress; u; u = u->Next) {
      if (u->Address.lpSockaddr->sa_family != AF_INET) continue;
      char text[INET_ADDRSTRLEN] = {};
      auto* in = reinterpret_cast<sockaddr_in*>(u->Address.lpSockaddr);
      inet_ntop(AF_INET, &in->sin_addr, text, sizeof(text));
      f(*a, std::string(text), u->OnLinkPrefixLength);
    }
  }
}

bool IsLinkLocal(const std::string& address) { return address.rfind("169.254.", 0) == 0; }

// The group's interface: the Wi-Fi Direct virtual adapter that is up, with its IPv4 address,
// the one Windows' connection sharing gives it rather than a link-local one (169.254.x.x/16,
// what it has when nothing serves addresses).
std::optional<HotspotInterface> FindGroupInterface() {
  std::optional<HotspotInterface> found;
  ForEachIPv4([&found](const IP_ADAPTER_ADDRESSES& a, const std::string& address, uint8_t prefix) {
    if (a.OperStatus != IfOperStatusUp || !a.Description) return;
    if (std::wstring(a.Description).find(L"Wi-Fi Direct Virtual Adapter") == std::wstring::npos) return;
    if (found && !IsLinkLocal(found->address)) return;
    HotspotInterface out;
    out.name = winrt::to_string(a.FriendlyName ? a.FriendlyName : L"");
    out.index = a.IfIndex;
    out.address = address;
    out.prefix = prefix;
    found = out;
  });
  return found;
}

// The address of the network Windows' connection sharing serves: 192.168.137.1, unless changed
// in the registry.
std::string SharingAddress() {
  wchar_t value[64] = {};
  DWORD size = sizeof(value);
  if (RegGetValueW(HKEY_LOCAL_MACHINE, L"SYSTEM\\CurrentControlSet\\Services\\SharedAccess\\Parameters",
                   L"ScopeAddress", RRF_RT_REG_SZ, nullptr, value, &size) != ERROR_SUCCESS ||
      !value[0]) {
    return "192.168.137.1";
  }
  return winrt::to_string(value);
}

// The card, other than the group's, that holds that address: a sharing to it, or one left
// behind (real test of 29 October 2026, an Ethernet card). "" if none.
std::string HolderOf(const std::string& address, uint32_t group_index) {
  std::string holder;
  ForEachIPv4([&](const IP_ADAPTER_ADDRESSES& a, const std::string& text, uint8_t) {
    if (holder.empty() && a.IfIndex != group_index && text == address) {
      holder = winrt::to_string(a.FriendlyName ? a.FriendlyName : L"");
    }
  });
  return holder;
}

}  // namespace

WifiDirectHotspot::~WifiDirectHotspot() { Stop(); }

winrt::fire_and_forget WifiDirectHotspot::Start(std::string ssid, std::string passphrase, Started done) {
  Stop();
  const uint64_t generation = generation_;
  // The window's thread: each wait leaves it and comes back to it, and the phones' requests are
  // accepted there.
  ui_.emplace();
  winrt::apartment_context ui = *ui_;
  WiFiDirectAdvertisementPublisher publisher{nullptr};
  // Gives up: the publisher stopped, the listener let go (unless a Stop did, and maybe a new
  // Start since), the error told.
  auto fail = [&](const std::string& error) {
    if (publisher) {
      try {
        publisher.Stop();
      } catch (const winrt::hresult_error&) {
      }
    }
    if (generation == generation_) Stop();
    done(error, HotspotInterface{});
  };
  try {
    publisher = WiFiDirectAdvertisementPublisher();
    auto advertisement = publisher.Advertisement();
    advertisement.IsAutonomousGroupOwnerEnabled(true);
    auto legacy = advertisement.LegacySettings();
    legacy.IsEnabled(true);
    legacy.Ssid(winrt::to_hstring(ssid));
    PasswordCredential credential;
    credential.Password(winrt::to_hstring(passphrase));
    legacy.Passphrase(credential);
    // Listening before the start, so that no request comes before it.
    WiFiDirectConnectionListener listener;
    requested_ = listener.ConnectionRequested(
        [this](const WiFiDirectConnectionListener&, const WiFiDirectConnectionRequestedEventArgs& args) {
          Accept(args.GetConnectionRequest());
        });
    listener_ = listener;
    // Off the window's thread too: Windows takes 0.2 to 1.4 s to start the group, the window
    // frozen as long when it waited there (real test of 30 October 2026).
    co_await winrt::resume_background();
    publisher.Start();
    co_await ui;
    if (generation != generation_) {
      fail("stopped");
      co_return;
    }
    // Started within a few milliseconds on the probe's PC; Aborted says the driver refused.
    bool started = false;
    for (int i = 0; i < 60 && !started; ++i) {
      auto status = publisher.Status();
      if (status == WiFiDirectAdvertisementPublisherStatus::Aborted) {
        fail("the Wi-Fi Direct group was refused (aborted)");
        co_return;
      }
      started = status == WiFiDirectAdvertisementPublisherStatus::Started;
      if (started) break;
      co_await winrt::resume_after(std::chrono::milliseconds(50));
      co_await ui;
      if (generation != generation_) {
        fail("stopped");
        co_return;
      }
    }
    if (!started) {
      fail("the Wi-Fi Direct group did not start");
      co_return;
    }
    // Its interface comes up a moment after, and the sharing's address a moment later still:
    // 4 s for the one, 3 more for the other.
    std::optional<HotspotInterface> group;
    for (int i = 0; i < 70; ++i) {
      group = FindGroupInterface();
      if ((group && !IsLinkLocal(group->address)) || (!group && i == 40)) break;
      co_await winrt::resume_after(std::chrono::milliseconds(100));
      co_await ui;
      if (generation != generation_) {
        fail("stopped");
        co_return;
      }
    }
    if (!group) {
      fail("the Wi-Fi Direct group has no interface");
      co_return;
    }
    HotspotInterface out = *group;
    out.windows_dhcp = !IsLinkLocal(group->address);
    if (!out.windows_dhcp) {
      // Accepting would fail after 30 s, and Windows drop the phone then: unanswered, it keeps
      // it 2 minutes, and the phone comes back by itself (WifiJoin.kt).
      listener_.ConnectionRequested(requested_);
      listener_ = nullptr;
      out.sharing_address = SharingAddress();
      out.held_by = HolderOf(out.sharing_address, group->index);
    }
    publisher_ = publisher;
    group_index_ = group->index;
    done(std::nullopt, out);
  } catch (const winrt::hresult_error& e) {
    fail(winrt::to_string(e.message()));
  }
}

winrt::fire_and_forget WifiDirectHotspot::Accept(WiFiDirectConnectionRequest request) {
  auto name = winrt::to_string(request.DeviceInformation().Name());
  // As Microsoft's Wi-Fi Direct sample does it: on the window's thread ("FromIdAsync needs to be
  // called from the UI thread"), without pairing for a client of the legacy settings. Then all
  // of this runs on that thread, as Start and Stop do. Accepted in 93 ms on the PC of the real
  // test of 29 October 2026, once Windows served the addresses.
  if (!ui_) co_return;
  co_await *ui_;
  try {
    WiFiDirectDevice device = co_await WiFiDirectDevice::FromIdAsync(request.DeviceInformation().Id());
    if (!device) {
      fprintf(stderr, "hotspot: %s not accepted\n", name.c_str());
      fflush(stderr);
    } else if (publisher_) {
      accepted_.push_back(device);
    }
  } catch (const winrt::hresult_error& e) {
    fprintf(stderr, "hotspot: %s not accepted: 0x%08X %s\n", name.c_str(), static_cast<unsigned>(e.code()),
            winrt::to_string(e.message()).c_str());
    fflush(stderr);
  }
}

void WifiDirectHotspot::Stop() {
  ++generation_;  // a Start still waiting gives up
  if (listener_) {
    listener_.ConnectionRequested(requested_);
    listener_ = nullptr;
  }
  for (auto& device : accepted_) device.Close();
  accepted_.clear();
  if (!publisher_) return;
  try {
    publisher_.Stop();  // 31 ms on the PC of the real test: on the window's thread
  } catch (const winrt::hresult_error&) {
    // Stopped already: nothing left to do.
  }
  publisher_ = nullptr;
  group_index_ = 0;
}

bool WifiDirectHotspot::Alive() {
  if (!publisher_) return false;
  auto group = FindGroupInterface();  // an adapter up, with an address
  return group && group->index == group_index_;
}
