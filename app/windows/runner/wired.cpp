#include "wired.h"

#include <winsock2.h>
#include <ws2tcpip.h>
#include <iphlpapi.h>
#include <netioapi.h>

namespace {

std::string Utf8(const wchar_t* w) {
  if (!w || !*w) return "";
  int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, nullptr, 0, nullptr, nullptr);
  std::string out(n > 0 ? n - 1 : 0, '\0');
  if (n > 1) WideCharToMultiByte(CP_UTF8, 0, w, -1, out.data(), n, nullptr, nullptr);
  return out;
}

// A physical card, with a connector: not Hyper-V's or VirtualBox's.
bool Physical(const IP_ADAPTER_ADDRESSES* aa) {
  MIB_IF_ROW2 row{};
  row.InterfaceLuid = aa->Luid;
  if (GetIfEntry2(&row) != NO_ERROR) return false;
  return row.InterfaceAndOperStatusFlags.HardwareInterface && row.InterfaceAndOperStatusFlags.ConnectorPresent;
}

}  // namespace

std::vector<WiredAddress> ManualWiredAddresses() {
  std::vector<WiredAddress> out;
  ULONG size = 16 * 1024;
  std::vector<unsigned char> buf;
  ULONG rc = ERROR_BUFFER_OVERFLOW;
  for (int i = 0; i < 3 && rc == ERROR_BUFFER_OVERFLOW; i++) {
    buf.resize(size);
    rc = GetAdaptersAddresses(AF_INET, GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER, nullptr,
                              reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data()), &size);
  }
  if (rc != NO_ERROR) return out;
  for (auto* aa = reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data()); aa; aa = aa->Next) {
    if (aa->IfType != IF_TYPE_ETHERNET_CSMACD || aa->OperStatus != IfOperStatusUp || !Physical(aa)) continue;
    for (auto* u = aa->FirstUnicastAddress; u; u = u->Next) {
      if (u->PrefixOrigin != IpPrefixOriginManual || u->Address.lpSockaddr->sa_family != AF_INET) continue;
      char text[INET_ADDRSTRLEN] = {};
      auto* in = reinterpret_cast<sockaddr_in*>(u->Address.lpSockaddr);
      if (!inet_ntop(AF_INET, &in->sin_addr, text, sizeof(text))) continue;
      out.push_back({Utf8(aa->FriendlyName), text});
    }
  }
  return out;
}
