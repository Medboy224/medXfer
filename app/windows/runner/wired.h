#ifndef RUNNER_WIRED_H_
#define RUNNER_WIRED_H_

#include <string>
#include <vector>

// An IPv4 address set by hand on a wired card with a link (lib/platform/cable.dart,
// WindowsCableLink). On a direct cable, the other PC reaches this one only with an address of
// the same network: the app says so (asked by the user on 2 November, after the address
// 192.168.137.1 of a stopped connection sharing kept the cable from working).
struct WiredAddress {
  std::string card;     // the adapter's name, as Windows shows it, UTF-8
  std::string address;  // dotted IPv4
};

// The manual IPv4 addresses of the physical wired cards that have a link. Virtual cards
// (Hyper-V, VirtualBox: often a fixed address on purpose) are left out.
std::vector<WiredAddress> ManualWiredAddresses();

#endif  // RUNNER_WIRED_H_
