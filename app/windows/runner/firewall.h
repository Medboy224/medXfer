#ifndef RUNNER_FIREWALL_H_
#define RUNNER_FIREWALL_H_

#include <string>
#include <vector>

// A network this computer is connected to, and whether Windows' firewall lets this program's
// incoming connections in on it (lib/platform/firewall.dart). Real test of 1 November: a PC let
// medXfer in on its cable only, the « Public » profile, and its Wi-Fi was « Private »: the other
// PC could neither connect to it nor receive from it, and nothing said why.
struct FirewallNetwork {
  std::string name;      // as Windows shows it, UTF-8
  std::string category;  // "public", "private" or "domain"
  bool allowed = true;
};

// The networks this computer is on, each with whether the firewall lets this program in, for
// TCP (its connections) and UDP (its discovery). Read without administrator rights.
std::vector<FirewallNetwork> FirewallStatus();

// Asks the user (UAC) to let this program in on every kind of network: its inbound rules are
// replaced by one that allows it, on every profile. False: refused by the user, or failed.
bool FirewallAllow();

#endif  // RUNNER_FIREWALL_H_
