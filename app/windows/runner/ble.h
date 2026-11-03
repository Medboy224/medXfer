#ifndef RUNNER_BLE_H_
#define RUNNER_BLE_H_

#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

#include <flutter/encodable_value.h>

#include <winrt/Windows.Devices.Bluetooth.Advertisement.h>
#include <winrt/Windows.Devices.Bluetooth.GenericAttributeProfile.h>
#include <winrt/Windows.Devices.Bluetooth.h>
#include <winrt/Windows.Foundation.h>

// The path by BLE (bible §6.6, WP-D.3 of the J6d plan), Windows' side: the same channel as
// Android's Ble.kt (lib/platform/ble.dart). This computer advertises medXfer's service, its UUID
// alone and hidden (no computer name, a random address: WP-D.0); answers the devices that connect
// to it (GATT server); finds the others (a watcher filtered on the service); and carries a message
// to one of them (GATT client: a write, then a read). The core makes and reads every message:
// this class only carries bytes. System APIs only (D5).
//
// Its methods are called on the window's thread, and its callbacks come back on it.
class Ble {
 public:
  // An event for Dart (seen, message, closed, advertise_failed, scan_failed), on the window's thread.
  using Emit = std::function<void(const std::string& event, flutter::EncodableMap args)>;
  using Done = std::function<void(std::optional<std::string> error)>;
  using Answer = std::function<void(std::optional<std::string> error, std::vector<uint8_t> answer)>;

  explicit Ble(Emit emit);
  ~Ble();

  // Whether the Bluetooth radio is on; asking Windows to turn it on, which it lets an app do
  // only if the user allows apps to control the radios (otherwise its settings open).
  winrt::fire_and_forget Enabled(std::function<void(bool on)> done);
  winrt::fire_and_forget TurnOn(std::function<void(bool on)> done);

  // Advertises and answers ([advertise]), scans ([scan]); done gets an error code, or nothing.
  winrt::fire_and_forget Start(bool advertise, bool scan, Done done);
  void Stop();

  // The answer [conn] reads next; a read already waiting gets it now.
  void Reply(const std::string& conn, std::vector<uint8_t> data);

  // Writes [message] to [device]'s service and reads its answer; connects first when needed.
  winrt::fire_and_forget Exchange(std::string device, std::vector<uint8_t> message, Answer done);
  void Disconnect(const std::string& device);

 private:
  struct Read;    // a read waiting for the core's answer
  struct Served;  // a device connected to this one
  struct Client;  // this one connected to a device

  winrt::fire_and_forget OnWrite(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattWriteRequestedEventArgs args);
  winrt::fire_and_forget OnRead(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattReadRequestedEventArgs args);
  winrt::fire_and_forget OnSeen(uint64_t address, int16_t rssi);
  winrt::fire_and_forget EmitOnUi(std::string event, flutter::EncodableMap args);
  Served& ServedLocked(const winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattSession& session);

  Emit emit_;
  std::optional<winrt::apartment_context> ui_;
  std::shared_ptr<bool> alive_ = std::make_shared<bool>(true);  // false once destroyed: late callbacks stop

  winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattServiceProvider provider_{nullptr};
  winrt::Windows::Devices::Bluetooth::Advertisement::BluetoothLEAdvertisementWatcher watcher_{nullptr};

  std::mutex mu_;  // the maps below: the server's events come on Windows' threads
  std::map<std::string, std::shared_ptr<Served>> served_;
  std::map<uint64_t, int64_t> seen_at_;  // ms of the last "seen" of each address
  std::map<std::string, std::shared_ptr<Client>> clients_;
};

#endif  // RUNNER_BLE_H_
