#include "ble.h"

#include <windows.h>
#include <shellapi.h>

#include <chrono>
#include <cstdio>

#include <winrt/Windows.Devices.Radios.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Storage.Streams.h>

using namespace winrt;
using namespace Windows::Devices::Bluetooth;
using namespace Windows::Devices::Bluetooth::Advertisement;
using namespace Windows::Devices::Bluetooth::GenericAttributeProfile;
using namespace Windows::Devices::Radios;
using namespace Windows::Storage::Streams;

namespace {

// Bible §6.6, BLE-02: medXfer's service and its one characteristic (as in Ble.kt).
const guid kService{0xc46b12c3, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
const guid kMessage{0xc46b12c4, 0x1dee, 0x482d, {0xa4, 0x90, 0xb1, 0x85, 0x21, 0x23, 0x12, 0xfb}};
constexpr size_t kMaxMessage = 512;                      // BLE-03
constexpr uint8_t kBleVersion = 1;                        // its messages' first byte (bible §6.6)
constexpr auto kAnswerWithin = std::chrono::seconds(25);  // BLE-11: under the 30 s of a GATT transaction
constexpr int64_t kSeenEveryMs = 2000;  // "seen" at most this often per address

std::vector<uint8_t> ToBytes(IBuffer const& b) {
  std::vector<uint8_t> v(b ? b.Length() : 0);
  if (!v.empty()) DataReader::FromBuffer(b).ReadBytes(v);
  return v;
}

IBuffer ToBuffer(const std::vector<uint8_t>& v, size_t from = 0) {
  DataWriter w;
  if (from < v.size()) w.WriteBytes(array_view<uint8_t const>(v.data() + from, v.data() + v.size()));
  return w.DetachBuffer();
}

std::string Address(uint64_t a) {
  char t[24];
  std::snprintf(t, sizeof(t), "%012llx", static_cast<unsigned long long>(a));
  return t;
}

int64_t NowMs() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now().time_since_epoch()).count();
}

flutter::EncodableValue V(const std::string& s) { return flutter::EncodableValue(s); }

Windows::Foundation::IAsyncOperation<Radio> BluetoothRadio() {
  auto adapter = co_await BluetoothAdapter::GetDefaultAsync();
  if (!adapter) co_return nullptr;
  co_return co_await adapter.GetRadioAsync();
}

}  // namespace

// A read that came before the core's answer: answered by Reply, or failed after kAnswerWithin.
struct Ble::Read {
  GattReadRequest request{nullptr};
  Windows::Foundation::Deferral deferral{nullptr};
  uint32_t offset = 0;
  bool answered = false;
};

// A device connected to this one's service.
struct Ble::Served {
  std::vector<uint8_t> writing;  // the message written, in pieces for a long write
  bool written = false;          // a message written and not yet given to the core
  std::optional<std::vector<uint8_t>> reply;
  std::vector<std::shared_ptr<Read>> waiting;
  GattSession session{nullptr};
  event_token status{};
};

// This computer connected to another device's service: one exchange at a time.
struct Ble::Client {
  BluetoothLEDevice device{nullptr};
  GattSession session{nullptr};
  GattCharacteristic characteristic{nullptr};
  bool busy = false;

  void Close() {
    if (session) session.Close();
    if (device) device.Close();
    session = nullptr;
    device = nullptr;
    characteristic = nullptr;
  }
};

Ble::Ble(Emit emit) : emit_(std::move(emit)) {}

Ble::~Ble() {
  *alive_ = false;
  Stop();
}

fire_and_forget Ble::Enabled(std::function<void(bool on)> done) {
  apartment_context ui;
  bool on = false;
  try {
    if (Radio radio = co_await BluetoothRadio()) on = radio.State() == RadioState::On;
  } catch (...) {
  }
  co_await ui;
  done(on);
}

fire_and_forget Ble::TurnOn(std::function<void(bool on)> done) {
  apartment_context ui;
  bool on = false;
  try {
    if (co_await Radio::RequestAccessAsync() == RadioAccessStatus::Allowed) {
      if (Radio radio = co_await BluetoothRadio()) {
        on = radio.State() == RadioState::On || co_await radio.SetStateAsync(RadioState::On) == RadioAccessStatus::Allowed;
      }
    }
  } catch (...) {
  }
  co_await ui;
  // Not allowed to: the user turns it on in Windows' settings.
  if (!on) ShellExecuteW(nullptr, L"open", L"ms-settings:bluetooth", nullptr, nullptr, SW_SHOWNORMAL);
  done(on);
}

fire_and_forget Ble::Start(bool advertise, bool scan, Done done) {
  ui_.emplace();
  apartment_context ui = *ui_;
  auto alive = alive_;
  std::optional<std::string> error;
  try {
    auto adapter = co_await BluetoothAdapter::GetDefaultAsync();
    Radio radio = adapter ? co_await adapter.GetRadioAsync() : nullptr;
    if (!adapter || !adapter.IsLowEnergySupported()) {
      error = "unsupported";
    } else if (!radio || radio.State() != RadioState::On) {
      error = "bluetooth_off";
    } else if (advertise && !provider_ && !adapter.IsPeripheralRoleSupported()) {
      error = "no_advertiser";
    }
    if (!error && advertise && !provider_) {
      auto created = co_await GattServiceProvider::CreateAsync(kService);
      co_await ui;
      if (!*alive) co_return;
      if (created.Error() != BluetoothError::Success) {
        error = "no_gatt_server";
      } else {
        auto provider = created.ServiceProvider();
        GattLocalCharacteristicParameters p;
        p.CharacteristicProperties(GattCharacteristicProperties::Write | GattCharacteristicProperties::Read);
        p.WriteProtectionLevel(GattProtectionLevel::Plain);
        p.ReadProtectionLevel(GattProtectionLevel::Plain);
        auto made = co_await provider.Service().CreateCharacteristicAsync(kMessage, p);
        co_await ui;
        if (!*alive) co_return;
        if (made.Error() != BluetoothError::Success) {
          error = "no_gatt_server";
        } else {
          auto characteristic = made.Characteristic();
          characteristic.WriteRequested([this, alive](auto&&, GattWriteRequestedEventArgs const& args) {
            if (*alive) OnWrite(args);
          });
          characteristic.ReadRequested([this, alive](auto&&, GattReadRequestedEventArgs const& args) {
            if (*alive) OnRead(args);
          });
          provider.AdvertisementStatusChanged([this, alive](auto&&, GattServiceProviderAdvertisementStatusChangedEventArgs const& e) {
            if (*alive && e.Status() == GattServiceProviderAdvertisementStatus::Aborted) {
              EmitOnUi("advertise_failed", {{V("code"), flutter::EncodableValue(static_cast<int32_t>(e.Error()))}});
            }
          });
          // Hidden (WP-D.0): not discoverable, so no computer name in the advertisement and a
          // random address that changes; the UUID alone (BLE-02).
          GattServiceProviderAdvertisingParameters params;
          params.IsConnectable(true);
          params.IsDiscoverable(false);
          // One byte of service data, the same on every computer (the BLE version): without it, the
          // Galaxy A57's scan filtered on the service never sees this computer, which a TECNO
          // does (3 November 2026: 0 results in 15 s without it, 11 and 12 with it).
          DataWriter version;
          version.WriteByte(kBleVersion);
          params.ServiceData(version.DetachBuffer());
          provider.StartAdvertising(params);
          provider_ = provider;
        }
      }
    }
    if (!error && scan && !watcher_) {
      BluetoothLEAdvertisementWatcher watcher;
      watcher.ScanningMode(BluetoothLEScanningMode::Active);
      watcher.AdvertisementFilter().Advertisement().ServiceUuids().Append(kService);
      watcher.Received([this, alive](auto&&, BluetoothLEAdvertisementReceivedEventArgs const& a) {
        if (*alive) OnSeen(a.BluetoothAddress(), a.RawSignalStrengthInDBm());
      });
      watcher.Stopped([this, alive](auto&&, BluetoothLEAdvertisementWatcherStoppedEventArgs const& e) {
        if (*alive && e.Error() != BluetoothError::Success) {
          EmitOnUi("scan_failed", {{V("code"), flutter::EncodableValue(static_cast<int32_t>(e.Error()))}});
        }
      });
      watcher.Start();
      watcher_ = watcher;
    }
  } catch (hresult_error const& e) {
    char t[16];
    std::snprintf(t, sizeof(t), "%08x", static_cast<unsigned>(e.code()));
    error = std::string("error_") + t;
  }
  co_await ui;
  if (*alive) done(error);
}

void Ble::Stop() {
  if (provider_) {
    try {
      provider_.StopAdvertising();
    } catch (...) {
    }
    provider_ = nullptr;
  }
  if (watcher_) {
    try {
      watcher_.Stop();
    } catch (...) {
    }
    watcher_ = nullptr;
  }
  std::lock_guard<std::mutex> lock(mu_);
  for (auto& [conn, s] : served_) {
    for (auto& r : s->waiting) {
      if (r->answered) continue;
      r->answered = true;
      try {
        r->request.RespondWithProtocolError(GattProtocolError::UnlikelyError());
      } catch (...) {
      }
      r->deferral.Complete();
    }
    if (s->session) s->session.SessionStatusChanged(s->status);
  }
  served_.clear();
  seen_at_.clear();
  for (auto& [device, c] : clients_) c->Close();
  clients_.clear();
}

fire_and_forget Ble::EmitOnUi(std::string event, flutter::EncodableMap args) {
  if (!ui_) co_return;
  auto alive = alive_;
  co_await *ui_;
  if (*alive) emit_(event, std::move(args));
}

fire_and_forget Ble::OnSeen(uint64_t address, int16_t rssi) {
  {
    std::lock_guard<std::mutex> lock(mu_);
    auto now = NowMs();
    auto it = seen_at_.find(address);
    if (it != seen_at_.end() && now - it->second < kSeenEveryMs) co_return;
    seen_at_[address] = now;
  }
  EmitOnUi("seen", {{V("device"), V(Address(address))}, {V("rssi"), flutter::EncodableValue(static_cast<int32_t>(rssi))}});
  co_return;
}

// The state of the device connected by session; mu_ held. Its going is said ("closed").
Ble::Served& Ble::ServedLocked(GattSession const& session) {
  auto conn = to_string(session.DeviceId().Id());
  auto& s = served_[conn];
  if (!s) {
    s = std::make_shared<Served>();
    s->session = session;
    auto alive = alive_;
    s->status = session.SessionStatusChanged([this, alive, conn](GattSession const&, GattSessionStatusChangedEventArgs const& e) {
      if (!*alive || e.Status() != GattSessionStatus::Closed) return;
      {
        std::lock_guard<std::mutex> lock(mu_);
        auto it = served_.find(conn);
        if (it == served_.end()) return;
        for (auto& r : it->second->waiting) {
          if (r->answered) continue;
          r->answered = true;
          r->deferral.Complete();
        }
        served_.erase(it);
      }
      EmitOnUi("closed", {{V("conn"), V(conn)}});
    });
  }
  return *s;
}

// A write from a device connected to this one: kept, in pieces for a long write, until it reads
// the answer, which marks the end of the message.
fire_and_forget Ble::OnWrite(GattWriteRequestedEventArgs args) {
  auto deferral = args.GetDeferral();
  auto alive = alive_;
  auto request = co_await args.GetRequestAsync();
  if (!*alive || !request) {
    deferral.Complete();
    co_return;
  }
  auto value = ToBytes(request.Value());
  bool ok = true;
  {
    std::lock_guard<std::mutex> lock(mu_);
    auto& s = ServedLocked(args.Session());
    if (request.Offset() == 0) {
      s.writing.clear();
      s.reply.reset();
    }
    if (request.Offset() != s.writing.size() || s.writing.size() + value.size() > kMaxMessage) {
      ok = false;
      s.writing.clear();
    } else {
      s.writing.insert(s.writing.end(), value.begin(), value.end());
      s.written = true;
    }
  }
  try {
    if (!ok) {
      request.RespondWithProtocolError(GattProtocolError::InvalidOffset());
    } else if (request.Option() == GattWriteOption::WriteWithResponse) {
      request.Respond();
    }
  } catch (...) {
  }
  deferral.Complete();
}

// A read: the core's answer when it is there; else the message written goes to the core, and the
// read waits for its answer (Reply), up to kAnswerWithin.
fire_and_forget Ble::OnRead(GattReadRequestedEventArgs args) {
  auto deferral = args.GetDeferral();
  auto alive = alive_;
  auto request = co_await args.GetRequestAsync();
  if (!*alive || !request) {
    deferral.Complete();
    co_return;
  }
  auto conn = to_string(args.Session().DeviceId().Id());
  auto read = std::make_shared<Read>();
  read->request = request;
  read->deferral = deferral;
  read->offset = request.Offset();
  std::optional<std::vector<uint8_t>> reply;
  std::optional<std::vector<uint8_t>> message;
  {
    std::lock_guard<std::mutex> lock(mu_);
    auto& s = ServedLocked(args.Session());
    if (s.reply) {
      reply = s.reply;
    } else {
      s.waiting.push_back(read);
      if (s.written) {
        message = s.writing;
        s.written = false;
      }
    }
  }
  if (reply) {
    try {
      request.RespondWithValue(ToBuffer(*reply, read->offset));
    } catch (...) {
    }
    deferral.Complete();
    co_return;
  }
  if (message) EmitOnUi("message", {{V("conn"), V(conn)}, {V("data"), flutter::EncodableValue(*message)}});
  co_await resume_after(kAnswerWithin);
  if (!*alive) co_return;
  std::lock_guard<std::mutex> lock(mu_);
  if (read->answered) co_return;
  read->answered = true;
  try {
    request.RespondWithProtocolError(GattProtocolError::UnlikelyError());
  } catch (...) {
  }
  deferral.Complete();
}

void Ble::Reply(const std::string& conn, std::vector<uint8_t> data) {
  std::lock_guard<std::mutex> lock(mu_);
  auto it = served_.find(conn);
  if (it == served_.end()) return;
  auto& s = *it->second;
  s.reply = data;
  for (auto& r : s.waiting) {
    if (r->answered) continue;
    r->answered = true;
    try {
      r->request.RespondWithValue(ToBuffer(data, r->offset));
    } catch (...) {
    }
    r->deferral.Complete();
  }
  s.waiting.clear();
}

fire_and_forget Ble::Exchange(std::string device, std::vector<uint8_t> message, Answer done) {
  apartment_context ui;
  auto alive = alive_;
  std::shared_ptr<Client> c;
  {
    std::lock_guard<std::mutex> lock(mu_);
    auto& slot = clients_[device];
    if (!slot) slot = std::make_shared<Client>();
    c = slot;
    if (c->busy) {
      done("busy", {});
      co_return;
    }
    c->busy = true;
  }
  std::optional<std::string> error;
  std::vector<uint8_t> answer;
  co_await resume_background();
  const char* step = "device";  // where an exception came from
  try {
    if (!c->characteristic) {
      c->device = co_await BluetoothLEDevice::FromBluetoothAddressAsync(std::stoull(device, nullptr, 16));
      if (!c->device) {
        error = "not_found";
      } else {
        step = "session";
        c->session = co_await GattSession::FromDeviceIdAsync(c->device.BluetoothDeviceId());
        c->session.MaintainConnection(true);
        // A full discovery first: asking for one service straight away failed against the
        // Galaxy A57 (WP-D.0). A phone paired in Bluetooth with this computer fails here, with
        // E_UNEXPECTED or "unreachable", where the same phone unpaired answers (real test of
        // 3 November 2026).
        step = "services";
        auto all = co_await c->device.GetGattServicesAsync(BluetoothCacheMode::Uncached);
        auto services = co_await c->device.GetGattServicesForUuidAsync(kService, BluetoothCacheMode::Uncached);
        if (services.Status() != GattCommunicationStatus::Success || services.Services().Size() == 0) {
          error = "no_service:" + std::to_string(static_cast<int>(all.Status())) + "/" +
                  std::to_string(all.Status() == GattCommunicationStatus::Success ? all.Services().Size() : 0) + "/" +
                  std::to_string(static_cast<int>(services.Status()));
        } else {
          step = "characteristic";
          auto found = co_await services.Services().GetAt(0).GetCharacteristicsForUuidAsync(kMessage, BluetoothCacheMode::Uncached);
          if (found.Status() != GattCommunicationStatus::Success || found.Characteristics().Size() == 0) {
            error = "no_characteristic:" + std::to_string(static_cast<int>(found.Status()));
          } else {
            c->characteristic = found.Characteristics().GetAt(0);
          }
        }
      }
    }
    // Longer than the MTU, Windows writes and reads in pieces (long write, long read).
    if (!error) {
      step = "write";
      auto written = co_await c->characteristic.WriteValueWithResultAsync(ToBuffer(message), GattWriteOption::WriteWithResponse);
      if (written.Status() != GattCommunicationStatus::Success) {
        error = "write_status:" + std::to_string(static_cast<int>(written.Status()));
      } else {
        step = "read";
        auto read = co_await c->characteristic.ReadValueAsync(BluetoothCacheMode::Uncached);
        if (read.Status() != GattCommunicationStatus::Success) {
          error = "read_status:" + std::to_string(static_cast<int>(read.Status()));
        } else {
          answer = ToBytes(read.Value());
        }
      }
    }
  } catch (hresult_error const& e) {
    char t[16];
    std::snprintf(t, sizeof(t), "%08x", static_cast<unsigned>(e.code()));
    error = std::string(step) + "_" + t;
  } catch (...) {
    error = std::string(step) + "_error";
  }
  if (error) c->Close();  // the next exchange connects again
  c->busy = false;
  co_await ui;
  if (*alive) done(error, std::move(answer));
}

void Ble::Disconnect(const std::string& device) {
  std::shared_ptr<Client> c;
  {
    std::lock_guard<std::mutex> lock(mu_);
    auto it = clients_.find(device);
    if (it == clients_.end()) return;
    c = it->second;
    clients_.erase(it);
  }
  c->Close();
}
