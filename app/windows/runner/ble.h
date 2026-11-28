#ifndef RUNNER_BLE_H_
#define RUNNER_BLE_H_

#include <atomic>
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
// A phone is never connected to by this computer (J8): paired with it in Bluetooth, Android left
// this computer's requests unanswered 20 to 50 s (real tests of 17 November 2026). The phone
// connects, and keeps its link while both look for devices, subscribed to the call
// characteristic ("held"): Exchange to that link notifies it, the phone reads this computer's
// message there, writes its answer, then reads once more, which marks the end.
//
// Its methods are called on the window's thread, and its callbacks come back on it.
class Ble {
 public:
  // An event for Dart (seen, held, message, closed, advertise_failed, scan_failed), on the window's thread.
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

  // Writes [message] to [device]'s service and reads its answer; connects first when needed. A
  // phone's held link ([device] its conn) is called instead.
  winrt::fire_and_forget Exchange(std::string device, std::vector<uint8_t> message, Answer done);
  void Disconnect(const std::string& device);

 private:
  struct Read;    // a read waiting for the core's answer
  struct Served;  // a device connected to this one
  struct Client;  // this one connected to a device
  struct Held;    // a phone's link kept with this one, by which this one calls it
  // Shared with background callbacks/coroutines so ~Ble() never destroys the mutex or ui while in use (#345).
  struct Gate {
    std::mutex mu;
    std::atomic<bool> alive{true};
    std::optional<winrt::apartment_context> ui;
  };

  winrt::fire_and_forget OnWrite(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattWriteRequestedEventArgs args,
                                 std::shared_ptr<Gate> gate);
  winrt::fire_and_forget OnRead(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattReadRequestedEventArgs args,
                                std::shared_ptr<Gate> gate);
  winrt::fire_and_forget OnSeen(uint64_t address, int16_t rssi, int type, bool holds, std::shared_ptr<Gate> gate);
  winrt::fire_and_forget OnCallWrite(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattWriteRequestedEventArgs args,
                                     std::shared_ptr<Gate> gate);
  winrt::fire_and_forget OnCallRead(winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattReadRequestedEventArgs args,
                                    std::shared_ptr<Gate> gate);
  void OnSubscribers(std::shared_ptr<Gate> gate);
  winrt::fire_and_forget Call(std::shared_ptr<Held> h, std::vector<uint8_t> message, Answer done, std::shared_ptr<Gate> gate);
  winrt::fire_and_forget AnswerOnUi(Answer done, std::optional<std::string> error, std::vector<uint8_t> answer,
                                    std::shared_ptr<Gate> gate);
  void DropHeldLocked(const std::string& conn, std::vector<Answer>& failed, bool& dropped);
  winrt::fire_and_forget EmitOnUi(std::string event, flutter::EncodableMap args, std::shared_ptr<Gate> gate);
  Served& ServedLocked(const winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattSession& session);

  Emit emit_;
  std::shared_ptr<Gate> gate_ = std::make_shared<Gate>();

  // The service, kept from one Start to the next: only its advertisement stops (Stop).
  winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattServiceProvider provider_{nullptr};
  winrt::Windows::Devices::Bluetooth::GenericAttributeProfile::GattLocalCharacteristic call_{nullptr};
  bool advertising_ = false;
  winrt::Windows::Devices::Bluetooth::Advertisement::BluetoothLEAdvertisementWatcher watcher_{nullptr};

  // Protected by gate_->mu: the server's events come on Windows' threads.
  std::map<std::string, std::shared_ptr<Served>> served_;
  std::map<uint64_t, int64_t> seen_at_;  // ms of the last "seen" of each address
  // The type of each address seen (BluetoothAddressType: 0 public, 1 random), for Exchange to
  // connect to it as advertised: a phone's address is random, which the address alone does not say.
  std::map<uint64_t, int> address_type_;
  std::map<std::string, std::shared_ptr<Client>> clients_;
  std::map<std::string, std::shared_ptr<Held>> held_;  // by the phone's conn
  std::map<uint64_t, bool> holds_;  // the addresses whose scan response says they connect to computers
};

#endif  // RUNNER_BLE_H_
