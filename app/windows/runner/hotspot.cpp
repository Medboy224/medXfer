#include "hotspot.h"

// winsock2 before windows.h: iphlpapi needs it.
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <iphlpapi.h>

#include <vector>

#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Security.Credentials.h>

using winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisher;
using winrt::Windows::Devices::WiFiDirect::WiFiDirectAdvertisementPublisherStatus;
using winrt::Windows::Security::Credentials::PasswordCredential;

namespace {

// The group's interface: the Wi-Fi Direct virtual adapter that is up, with its IPv4 address
// (Windows gives it a link-local one, 169.254.x.x/16, when nothing serves addresses).
std::optional<HotspotInterface> FindGroupInterface() {
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
  if (rc != NO_ERROR) return std::nullopt;
  for (auto* a = list; a; a = a->Next) {
    if (a->OperStatus != IfOperStatusUp || !a->Description) continue;
    if (std::wstring(a->Description).find(L"Wi-Fi Direct Virtual Adapter") == std::wstring::npos) continue;
    for (auto* u = a->FirstUnicastAddress; u; u = u->Next) {
      if (u->Address.lpSockaddr->sa_family != AF_INET) continue;
      char text[INET_ADDRSTRLEN] = {};
      auto* in = reinterpret_cast<sockaddr_in*>(u->Address.lpSockaddr);
      inet_ntop(AF_INET, &in->sin_addr, text, sizeof(text));
      HotspotInterface out;
      out.name = winrt::to_string(a->FriendlyName ? a->FriendlyName : L"");
      out.index = a->IfIndex;
      out.address = text;
      out.prefix = u->OnLinkPrefixLength;
      return out;
    }
  }
  return std::nullopt;
}

}  // namespace

WifiDirectHotspot::~WifiDirectHotspot() { Stop(); }

std::optional<std::string> WifiDirectHotspot::Start(const std::string& ssid,
                                                    const std::string& passphrase,
                                                    HotspotInterface* out) {
  Stop();
  try {
    WiFiDirectAdvertisementPublisher publisher;
    auto advertisement = publisher.Advertisement();
    advertisement.IsAutonomousGroupOwnerEnabled(true);
    auto legacy = advertisement.LegacySettings();
    legacy.IsEnabled(true);
    legacy.Ssid(winrt::to_hstring(ssid));
    PasswordCredential credential;
    credential.Password(winrt::to_hstring(passphrase));
    legacy.Passphrase(credential);
    publisher.Start();
    // Started within a few milliseconds on the probe's PC; Aborted says the driver refused.
    bool started = false;
    for (int i = 0; i < 60 && !started; ++i) {
      auto status = publisher.Status();
      if (status == WiFiDirectAdvertisementPublisherStatus::Aborted) {
        return "the Wi-Fi Direct group was refused (aborted)";
      }
      started = status == WiFiDirectAdvertisementPublisherStatus::Started;
      if (!started) Sleep(50);
    }
    if (!started) {
      publisher.Stop();
      return "the Wi-Fi Direct group did not start";
    }
    // Its interface comes up a moment after.
    for (int i = 0; i < 40; ++i) {
      if (auto found = FindGroupInterface()) {
        *out = *found;
        publisher_ = publisher;
        return std::nullopt;
      }
      Sleep(100);
    }
    publisher.Stop();
    return "the Wi-Fi Direct group has no interface";
  } catch (const winrt::hresult_error& e) {
    return winrt::to_string(e.message());
  }
}

void WifiDirectHotspot::Stop() {
  if (!publisher_) return;
  try {
    publisher_.Stop();
  } catch (const winrt::hresult_error&) {
    // Stopped already: nothing left to do.
  }
  publisher_ = nullptr;
}
