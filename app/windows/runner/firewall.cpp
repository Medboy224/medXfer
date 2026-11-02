#include "firewall.h"

#include <windows.h>
#include <netfw.h>
#include <netlistmgr.h>
#include <oleauto.h>
#include <shellapi.h>
#include <wrl/client.h>

using Microsoft::WRL::ComPtr;

namespace {

std::wstring ProgramPath() {
  std::wstring path(MAX_PATH, L'\0');
  for (;;) {
    DWORD n = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
    if (n < path.size()) {
      path.resize(n);
      return path;
    }
    path.resize(path.size() * 2);
  }
}

std::string Utf8(const wchar_t* w) {
  if (!w || !*w) return "";
  int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, nullptr, 0, nullptr, nullptr);
  std::string out(n > 0 ? n - 1 : 0, '\0');
  if (n > 1) WideCharToMultiByte(CP_UTF8, 0, w, -1, out.data(), n, nullptr, nullptr);
  return out;
}

// A rule's program may name an environment variable (%USERPROFILE%\...).
std::wstring Expanded(const wchar_t* s) {
  DWORD n = ExpandEnvironmentStringsW(s, nullptr, 0);
  if (n == 0) return s;
  std::wstring out(n, L'\0');
  ExpandEnvironmentStringsW(s, out.data(), n);
  out.resize(n - 1);
  return out;
}

bool SamePath(const std::wstring& a, const std::wstring& b) {
  return CompareStringOrdinal(a.c_str(), -1, b.c_str(), -1, TRUE) == CSTR_EQUAL;
}

// The firewall's profile for a network's category.
long ProfileOf(NLM_NETWORK_CATEGORY category) {
  switch (category) {
    case NLM_NETWORK_CATEGORY_PRIVATE:
      return NET_FW_PROFILE2_PRIVATE;
    case NLM_NETWORK_CATEGORY_DOMAIN_AUTHENTICATED:
      return NET_FW_PROFILE2_DOMAIN;
    default:
      return NET_FW_PROFILE2_PUBLIC;
  }
}

// The profiles where an enabled inbound rule of this program allows, or blocks, TCP and UDP.
struct ProgramRules {
  long allow_tcp = 0, allow_udp = 0, block = 0;
};

ProgramRules RulesOf(INetFwPolicy2* policy, const std::wstring& self) {
  ProgramRules out;
  ComPtr<INetFwRules> rules;
  ComPtr<IUnknown> unknown;
  ComPtr<IEnumVARIANT> each;
  if (FAILED(policy->get_Rules(&rules)) || FAILED(rules->get__NewEnum(&unknown)) || FAILED(unknown.As(&each))) {
    return out;
  }
  VARIANT v;
  VariantInit(&v);
  ULONG fetched = 0;
  while (each->Next(1, &v, &fetched) == S_OK) {
    ComPtr<INetFwRule> rule;
    if (v.vt == VT_DISPATCH && v.pdispVal && SUCCEEDED(v.pdispVal->QueryInterface(IID_PPV_ARGS(&rule)))) {
      BSTR app = nullptr;
      VARIANT_BOOL enabled = VARIANT_FALSE;
      NET_FW_RULE_DIRECTION direction = NET_FW_RULE_DIR_OUT;
      NET_FW_ACTION action = NET_FW_ACTION_BLOCK;
      long profiles = 0, protocol = 0;
      if (SUCCEEDED(rule->get_ApplicationName(&app)) && app && SamePath(Expanded(app), self) &&
          SUCCEEDED(rule->get_Enabled(&enabled)) && enabled == VARIANT_TRUE &&
          SUCCEEDED(rule->get_Direction(&direction)) && direction == NET_FW_RULE_DIR_IN &&
          SUCCEEDED(rule->get_Action(&action)) && SUCCEEDED(rule->get_Profiles(&profiles)) &&
          SUCCEEDED(rule->get_Protocol(&protocol))) {
        const bool tcp = protocol == NET_FW_IP_PROTOCOL_TCP || protocol == NET_FW_IP_PROTOCOL_ANY;
        const bool udp = protocol == NET_FW_IP_PROTOCOL_UDP || protocol == NET_FW_IP_PROTOCOL_ANY;
        if (action == NET_FW_ACTION_ALLOW) {
          if (tcp) out.allow_tcp |= profiles;
          if (udp) out.allow_udp |= profiles;
        } else if (tcp || udp) {
          out.block |= profiles;
        }
      }
      if (app) SysFreeString(app);
    }
    VariantClear(&v);
  }
  return out;
}

}  // namespace

std::vector<FirewallNetwork> FirewallStatus() {
  std::vector<FirewallNetwork> out;
  // COM runs on the window's thread (main.cpp), where the method channel calls this.
  ComPtr<INetworkListManager> manager;
  ComPtr<IEnumNetworks> networks;
  if (FAILED(CoCreateInstance(__uuidof(NetworkListManager), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&manager))) ||
      FAILED(manager->GetNetworks(NLM_ENUM_NETWORK_CONNECTED, &networks))) {
    return out;
  }
  ComPtr<INetFwPolicy2> policy;
  const bool known = SUCCEEDED(CoCreateInstance(__uuidof(NetFwPolicy2), nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&policy)));
  const ProgramRules rules = known ? RulesOf(policy.Get(), ProgramPath()) : ProgramRules{};

  ComPtr<INetwork> network;
  ULONG fetched = 0;
  while (networks->Next(1, &network, &fetched) == S_OK) {
    BSTR name = nullptr;
    NLM_NETWORK_CATEGORY category = NLM_NETWORK_CATEGORY_PUBLIC;
    network->GetName(&name);
    network->GetCategory(&category);
    FirewallNetwork n;
    n.name = Utf8(name);
    if (name) SysFreeString(name);
    n.category = category == NLM_NETWORK_CATEGORY_PRIVATE                ? "private"
                 : category == NLM_NETWORK_CATEGORY_DOMAIN_AUTHENTICATED ? "domain"
                                                                         : "public";
    if (known) {
      const long profile = ProfileOf(category);
      const auto type = static_cast<NET_FW_PROFILE_TYPE2>(profile);
      VARIANT_BOOL on = VARIANT_TRUE, shields_up = VARIANT_FALSE;
      policy->get_FirewallEnabled(type, &on);
      policy->get_BlockAllInboundTraffic(type, &shields_up);
      n.allowed = on == VARIANT_FALSE ||
                  (shields_up == VARIANT_FALSE && (rules.allow_tcp & profile) && (rules.allow_udp & profile) &&
                   !(rules.block & profile));
    }
    out.push_back(n);
    network.Reset();
  }
  return out;
}

bool FirewallAllow() {
  const std::wstring self = ProgramPath();
  // One elevated command, one prompt: this program's inbound rules go (a « block » left by a
  // refused prompt would win over any allow), then one rule lets it in, every protocol, every
  // profile. netsh fails the delete when there is none to delete: the add runs all the same.
  const std::wstring params = L"/c netsh advfirewall firewall delete rule name=all dir=in program=\"" + self +
                              L"\" & netsh advfirewall firewall add rule name=\"medXfer\" dir=in action=allow program=\"" +
                              self + L"\" enable=yes profile=any";
  SHELLEXECUTEINFOW info{};
  info.cbSize = sizeof(info);
  info.fMask = SEE_MASK_NOCLOSEPROCESS;
  info.lpVerb = L"runas";
  info.lpFile = L"cmd.exe";
  info.lpParameters = params.c_str();
  info.nShow = SW_HIDE;
  if (!ShellExecuteExW(&info) || !info.hProcess) {
    return false;  // refused at the prompt (ERROR_CANCELLED), or no elevation possible
  }
  WaitForSingleObject(info.hProcess, 30000);
  DWORD code = 1;
  GetExitCodeProcess(info.hProcess, &code);
  CloseHandle(info.hProcess);
  return code == 0;
}
