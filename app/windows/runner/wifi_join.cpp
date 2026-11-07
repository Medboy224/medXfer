#include "wifi_join.h"

// winsock2 before windows.h: iphlpapi needs it.
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <iphlpapi.h>
#include <wlanapi.h>

#include <chrono>
#include <cstdio>
#include <thread>
#include <vector>

namespace {

// An open handle to Windows' Wi-Fi service, closed at the end of its scope.
class Wlan {
 public:
  Wlan() {
    DWORD version = 0;
    if (WlanOpenHandle(2, nullptr, &version, &h_) != ERROR_SUCCESS) h_ = nullptr;
  }
  ~Wlan() {
    if (h_) WlanCloseHandle(h_, nullptr);
  }
  HANDLE get() const { return h_; }

  // The computer's Wi-Fi card: the one connected, else the first. Wi-Fi Direct's virtual
  // interfaces are not listed.
  std::optional<GUID> Card() const {
    if (!h_) return std::nullopt;
    WLAN_INTERFACE_INFO_LIST* list = nullptr;
    if (WlanEnumInterfaces(h_, nullptr, &list) != ERROR_SUCCESS || !list) return std::nullopt;
    std::optional<GUID> card;
    for (DWORD i = 0; i < list->dwNumberOfItems; i++) {
      if (!card || list->InterfaceInfo[i].isState == wlan_interface_state_connected) card = list->InterfaceInfo[i].InterfaceGuid;
      if (list->InterfaceInfo[i].isState == wlan_interface_state_connected) break;
    }
    WlanFreeMemory(list);
    return card;
  }

  bool RadioOn(const GUID& card) const {
    DWORD size = 0;
    WLAN_RADIO_STATE* state = nullptr;
    if (WlanQueryInterface(h_, &card, wlan_intf_opcode_radio_state, nullptr, &size, reinterpret_cast<void**>(&state), nullptr) != ERROR_SUCCESS) {
      return true;  // not said: Windows refuses the connection if it is off
    }
    bool on = false;
    for (DWORD i = 0; i < state->dwNumberOfPhys; i++) {
      on |= state->PhyRadioState[i].dot11SoftwareRadioState == dot11_radio_state_on &&
            state->PhyRadioState[i].dot11HardwareRadioState == dot11_radio_state_on;
    }
    WlanFreeMemory(state);
    return on;
  }

  // Whether the card joins 5 GHz: 802.11a, ac or ax among its kinds.
  bool Has5GHz(const GUID& card) const {
    WLAN_INTERFACE_CAPABILITY* cap = nullptr;
    if (WlanGetInterfaceCapability(h_, &card, nullptr, &cap) != ERROR_SUCCESS || !cap) return false;
    bool five = false;
    for (DWORD i = 0; i < cap->dwNumberOfSupportedPhys && i < WLAN_MAX_PHY_INDEX; i++) {
      auto t = cap->dot11PhyTypes[i];
      five |= t == dot11_phy_type_ofdm || t == dot11_phy_type_vht || t == dot11_phy_type_he;
    }
    WlanFreeMemory(cap);
    return five;
  }

  // The profile of the network the card is connected to; empty for none.
  std::wstring Connected(const GUID& card) const {
    DWORD size = 0;
    WLAN_CONNECTION_ATTRIBUTES* c = nullptr;
    if (WlanQueryInterface(h_, &card, wlan_intf_opcode_current_connection, nullptr, &size, reinterpret_cast<void**>(&c), nullptr) != ERROR_SUCCESS) {
      return L"";
    }
    std::wstring profile = c->isState == wlan_interface_state_connected ? c->strProfileName : L"";
    WlanFreeMemory(c);
    return profile;
  }

  uint32_t Frequency(const GUID& card) const {
    DWORD size = 0;
    ULONG* channel = nullptr;
    if (WlanQueryInterface(h_, &card, wlan_intf_opcode_channel_number, nullptr, &size, reinterpret_cast<void**>(&channel), nullptr) != ERROR_SUCCESS) {
      return 0;
    }
    ULONG ch = *channel;
    WlanFreeMemory(channel);
    if (ch == 14) return 2484;
    return ch < 14 ? 2407 + 5 * ch : 5000 + 5 * ch;
  }

  bool Connect(const GUID& card, const std::wstring& profile) const {
    WLAN_CONNECTION_PARAMETERS p{};
    p.wlanConnectionMode = wlan_connection_mode_profile;
    p.strProfile = profile.c_str();
    p.dot11BssType = dot11_BSS_type_infrastructure;
    return WlanConnect(h_, &card, &p, nullptr) == ERROR_SUCCESS;
  }

 private:
  HANDLE h_ = nullptr;
};

std::wstring Wide(const std::string& s) {
  if (s.empty()) return L"";
  int n = MultiByteToWideChar(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), nullptr, 0);
  std::wstring w(n, L'\0');
  MultiByteToWideChar(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), w.data(), n);
  return w;
}

std::string Narrow(const wchar_t* w) {
  int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, nullptr, 0, nullptr, nullptr);
  if (n <= 1) return "";
  std::string s(n - 1, '\0');
  WideCharToMultiByte(CP_UTF8, 0, w, -1, s.data(), n, nullptr, nullptr);
  return s;
}

std::string Xml(const std::string& s) {
  std::string out;
  for (char c : s) {
    switch (c) {
      case '&': out += "&amp;"; break;
      case '<': out += "&lt;"; break;
      case '>': out += "&gt;"; break;
      case '"': out += "&quot;"; break;
      case '\'': out += "&apos;"; break;
      default: out += c;
    }
  }
  return out;
}

// A profile of this user's for [ssid]: joined only when the app asks (manual), its passphrase
// in clear for Windows to protect when it stores it.
std::string Profile(const std::string& name, const std::string& ssid, const std::string& passphrase, int security) {
  std::string hex;
  for (unsigned char c : ssid) {
    char t[3];
    std::snprintf(t, sizeof(t), "%02X", c);
    hex += t;
  }
  // WPA3 transition networks take WPA2 too.
  const char* auth = security == 3 ? "WPA3SAE" : "WPA2PSK";
  return "<?xml version=\"1.0\"?>"
         "<WLANProfile xmlns=\"http://www.microsoft.com/networking/WLAN/profile/v1\">"
         "<name>" + Xml(name) + "</name>"
         "<SSIDConfig><SSID><hex>" + hex + "</hex><name>" + Xml(ssid) + "</name></SSID></SSIDConfig>"
         "<connectionType>ESS</connectionType><connectionMode>manual</connectionMode>"
         "<MSM><security><authEncryption><authentication>" + auth + "</authentication><encryption>AES</encryption>"
         "<useOneX>false</useOneX></authEncryption><sharedKey><keyType>passPhrase</keyType><protected>false</protected>"
         "<keyMaterial>" + Xml(passphrase) + "</keyMaterial></sharedKey></security></MSM>"
         "</WLANProfile>";
}

// The card's IPv4 address, once the network's address server gave it one.
std::optional<JoinedNetwork> AddressOf(const GUID& card) {
  NET_LUID luid{};
  if (ConvertInterfaceGuidToLuid(&card, &luid) != NO_ERROR) return std::nullopt;
  NET_IFINDEX index = 0;
  if (ConvertInterfaceLuidToIndex(&luid, &index) != NO_ERROR) return std::nullopt;
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
    if (a->IfIndex != index) continue;
    for (auto* u = a->FirstUnicastAddress; u; u = u->Next) {
      char text[INET_ADDRSTRLEN] = {};
      inet_ntop(AF_INET, &reinterpret_cast<sockaddr_in*>(u->Address.lpSockaddr)->sin_addr, text, sizeof(text));
      std::string address(text);
      if (address.rfind("169.254.", 0) == 0) continue;  // none served yet
      JoinedNetwork j;
      j.name = Narrow(a->FriendlyName);
      j.index = index;
      j.address = address;
      j.prefix = u->OnLinkPrefixLength;
      return j;
    }
  }
  return std::nullopt;
}

}  // namespace

WifiBandMask OwnWifiBands() {
  Wlan wlan;
  auto card = wlan.Card();
  if (!card) return {};
  // Wi-Fi off: no network joined, and none created either, the group needing Wi-Fi (#219).
  if (!wlan.RadioOn(*card)) return {};
  WifiBandMask m;
  m.create = 1;
  m.join = 1 | (wlan.Has5GHz(*card) ? 2 : 0);
  return m;
}

WifiJoin::~WifiJoin() { Leave(); }

winrt::fire_and_forget WifiJoin::Join(std::string ssid, std::string passphrase, int security, char band,
                                      std::function<void(std::optional<std::string> error, JoinedNetwork joined)> done) {
  winrt::apartment_context ui;
  Leave();
  std::wstring profile = Wide("medXfer " + ssid);
  std::wstring previous;
  GUID card{};
  std::optional<std::string> error;
  JoinedNetwork joined;
  co_await winrt::resume_background();
  {
    Wlan wlan;
    auto found = wlan.Card();
    if (!wlan.get() || !found) {
      error = "no_wifi";
    } else if (card = *found; !wlan.RadioOn(card)) {
      error = "wifi_off";
    } else if (band == '5' && !wlan.Has5GHz(card)) {
      error = "no_band";
    } else {
      previous = wlan.Connected(card);
      DWORD reason = 0;
      auto xml = Wide(Profile(Narrow(profile.c_str()), ssid, passphrase, security));
      if (WlanSetProfile(wlan.get(), &card, WLAN_PROFILE_USER, xml.c_str(), nullptr, TRUE, nullptr, &reason) != ERROR_SUCCESS ||
          !wlan.Connect(card, profile)) {
        error = "join_failed";
      } else {
        // Connected within 20 s, then an address within 15 more.
        auto until = std::chrono::steady_clock::now() + std::chrono::seconds(20);
        while (wlan.Connected(card) != profile && std::chrono::steady_clock::now() < until) {
          std::this_thread::sleep_for(std::chrono::milliseconds(250));
        }
        if (wlan.Connected(card) != profile) {
          error = "join_failed";
        } else {
          until = std::chrono::steady_clock::now() + std::chrono::seconds(15);
          std::optional<JoinedNetwork> got;
          while (!(got = AddressOf(card)) && std::chrono::steady_clock::now() < until) {
            std::this_thread::sleep_for(std::chrono::milliseconds(250));
          }
          if (!got) {
            error = "no_address";
          } else {
            joined = *got;
            joined.frequency = wlan.Frequency(card);
          }
        }
      }
      if (error) {
        WlanDeleteProfile(wlan.get(), &card, profile.c_str(), nullptr);
        if (!previous.empty()) wlan.Connect(card, previous);
      }
    }
  }
  co_await ui;
  if (!error) {
    profile_ = profile;
    previous_ = previous;
    card_ = card;
  }
  done(error, joined);
}

void WifiJoin::Leave() {
  if (profile_.empty()) return;
  Wlan wlan;
  if (wlan.get()) {
    WlanDisconnect(wlan.get(), &card_, nullptr);
    WlanDeleteProfile(wlan.get(), &card_, profile_.c_str(), nullptr);
    if (!previous_.empty() && previous_ != profile_) wlan.Connect(card_, previous_);
  }
  profile_.clear();
  previous_.clear();
}
