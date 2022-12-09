/*
 * Copyright 2018-present Open Networking Foundation

 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at

 * http://www.apache.org/licenses/LICENSE-2.0

 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

//Package core provides the utility for olt devices, flows and statistics
package core

import (
	"context"
	"errors"
	"sync"
	"time"
  "fmt"
  "strings"
	"github.com/golang/protobuf/ptypes/empty"
	conf "github.com/opencord/voltha-lib-go/v7/pkg/config"
	"github.com/opencord/voltha-lib-go/v7/pkg/events/eventif"
	vgrpc "github.com/opencord/voltha-lib-go/v7/pkg/grpc"
	"github.com/opencord/voltha-lib-go/v7/pkg/log"
	"github.com/opencord/voltha-openolt-adapter/internal/pkg/config"
	"github.com/opencord/voltha-openolt-adapter/internal/pkg/olterrors"
	"github.com/opencord/voltha-protos/v5/go/common"
	ca "github.com/opencord/voltha-protos/v5/go/core_adapter"
	"github.com/opencord/voltha-protos/v5/go/extension"
	"github.com/opencord/voltha-protos/v5/go/health"
	ia "github.com/opencord/voltha-protos/v5/go/inter_adapter"
	"github.com/opencord/voltha-protos/v5/go/omci"
	"github.com/opencord/voltha-protos/v5/go/voltha"
	"github.com/opencord/voltha-protos/v5/go/bossopenolt"
)

//OpenOLT structure holds the OLT information
type OpenOLT struct {
	configManager               *conf.ConfigManager
	deviceHandlers              map[string]*DeviceHandler
	coreClient                  *vgrpc.Client
	eventProxy                  eventif.EventProxy
	config                      *config.AdapterFlags
	numOnus                     int
	KVStoreAddress              string
	KVStoreType                 string
	exitChannel                 chan int
	HeartbeatCheckInterval      time.Duration
	HeartbeatFailReportInterval time.Duration
	GrpcTimeoutInterval         time.Duration
	lockDeviceHandlersMap       sync.RWMutex
	enableONUStats              bool
	enableGemStats              bool
	rpcTimeout                  time.Duration
}

//NewOpenOLT returns a new instance of OpenOLT
func NewOpenOLT(ctx context.Context,
	coreClient *vgrpc.Client,
	eventProxy eventif.EventProxy, cfg *config.AdapterFlags, cm *conf.ConfigManager) *OpenOLT {
	var openOLT OpenOLT
	openOLT.exitChannel = make(chan int, 1)
	openOLT.deviceHandlers = make(map[string]*DeviceHandler)
	openOLT.config = cfg
	openOLT.numOnus = cfg.OnuNumber
	openOLT.coreClient = coreClient
	openOLT.eventProxy = eventProxy
	openOLT.KVStoreAddress = cfg.KVStoreAddress
	openOLT.KVStoreType = cfg.KVStoreType
	openOLT.HeartbeatCheckInterval = cfg.HeartbeatCheckInterval
	openOLT.HeartbeatFailReportInterval = cfg.HeartbeatFailReportInterval
	openOLT.GrpcTimeoutInterval = cfg.GrpcTimeoutInterval
	openOLT.lockDeviceHandlersMap = sync.RWMutex{}
	openOLT.configManager = cm
	openOLT.enableONUStats = cfg.EnableONUStats
	openOLT.enableGemStats = cfg.EnableGEMStats
	openOLT.rpcTimeout = cfg.RPCTimeout
	return &openOLT
}

//Start starts (logs) the device manager
func (oo *OpenOLT) Start(ctx context.Context) error {
	logger.Info(ctx, "starting-device-manager")
	logger.Info(ctx, "device-manager-started")
	return nil
}

//Stop terminates the session
func (oo *OpenOLT) Stop(ctx context.Context) error {
	logger.Info(ctx, "stopping-device-manager")
	oo.exitChannel <- 1
	logger.Info(ctx, "device-manager-stopped")
	return nil
}

func (oo *OpenOLT) addDeviceHandlerToMap(agent *DeviceHandler) {
	oo.lockDeviceHandlersMap.Lock()
	defer oo.lockDeviceHandlersMap.Unlock()
	if _, exist := oo.deviceHandlers[agent.device.Id]; !exist {
		oo.deviceHandlers[agent.device.Id] = agent
	}
}

func (oo *OpenOLT) deleteDeviceHandlerToMap(agent *DeviceHandler) {
	oo.lockDeviceHandlersMap.Lock()
	defer oo.lockDeviceHandlersMap.Unlock()
	delete(oo.deviceHandlers, agent.device.Id)
}

func (oo *OpenOLT) getDeviceHandler(deviceID string) *DeviceHandler {
	oo.lockDeviceHandlersMap.Lock()
	defer oo.lockDeviceHandlersMap.Unlock()
	if agent, ok := oo.deviceHandlers[deviceID]; ok {
		return agent
	}
	return nil
}

// GetHealthStatus is used as a service readiness validation as a grpc connection
func (oo *OpenOLT) GetHealthStatus(ctx context.Context, clientConn *common.Connection) (*health.HealthStatus, error) {
	return &health.HealthStatus{State: health.HealthStatus_HEALTHY}, nil
}

// AdoptDevice creates a new device handler if not present already and then adopts the device
func (oo *OpenOLT) AdoptDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	if device == nil {
		return nil, olterrors.NewErrInvalidValue(log.Fields{"device": nil}, nil).Log()
	}
	logger.Infow(ctx, "adopt-device", log.Fields{"device-id": device.Id})
	var handler *DeviceHandler
	if handler = oo.getDeviceHandler(device.Id); handler == nil {
		handler := NewDeviceHandler(oo.coreClient, oo.eventProxy, device, oo, oo.configManager, oo.config)
		oo.addDeviceHandlerToMap(handler)
		go handler.AdoptDevice(log.WithSpanFromContext(context.Background(), ctx), device)
	}
	return &empty.Empty{}, nil
}

//GetOfpDeviceInfo returns OFP information for the given device
func (oo *OpenOLT) GetOfpDeviceInfo(ctx context.Context, device *voltha.Device) (*ca.SwitchCapability, error) {
	logger.Infow(ctx, "get_ofp_device_info", log.Fields{"device-id": device.Id})
	if handler := oo.getDeviceHandler(device.Id); handler != nil {
		return handler.GetOfpDeviceInfo(device)
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": device.Id}, nil)
}

//ReconcileDevice unimplemented
func (oo *OpenOLT) ReconcileDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	if device == nil {
		return nil, olterrors.NewErrInvalidValue(log.Fields{"device": nil}, nil)
	}
	logger.Infow(ctx, "reconcile-device", log.Fields{"device-id": device.Id})
	var handler *DeviceHandler
	if handler = oo.getDeviceHandler(device.Id); handler == nil {
		//Setting state to RECONCILING
		cgClient, err := oo.coreClient.GetCoreServiceClient()
		if err != nil {
			return nil, err
		}
		subCtx, cancel := context.WithTimeout(log.WithSpanFromContext(context.Background(), ctx), oo.rpcTimeout)
		defer cancel()
		if _, err := cgClient.DeviceStateUpdate(subCtx, &ca.DeviceStateFilter{
			DeviceId:   device.Id,
			OperStatus: voltha.OperStatus_RECONCILING,
			ConnStatus: device.ConnectStatus,
		}); err != nil {
			return nil, olterrors.NewErrAdapter("device-update-failed", log.Fields{"device-id": device.Id}, err)
		}

		// The OperState of the device is set to RECONCILING in the previous section. This also needs to be set on the
		// locally cached copy of the device struct.
		device.OperStatus = voltha.OperStatus_RECONCILING
		handler := NewDeviceHandler(oo.coreClient, oo.eventProxy, device, oo, oo.configManager, oo.config)
		handler.adapterPreviouslyConnected = true
		oo.addDeviceHandlerToMap(handler)
		handler.transitionMap = NewTransitionMap(handler)

		handler.transitionMap.Handle(log.WithSpanFromContext(context.Background(), ctx), DeviceInit)
	}
	return &empty.Empty{}, nil
}

//DisableDevice disables the given device
func (oo *OpenOLT) DisableDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	logger.Infow(ctx, "disable-device", log.Fields{"device-id": device.Id})
	if handler := oo.getDeviceHandler(device.Id); handler != nil {
		if err := handler.DisableDevice(log.WithSpanFromContext(context.Background(), ctx), device); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": device.Id}, nil)
}

//ReEnableDevice enables the olt device after disable
func (oo *OpenOLT) ReEnableDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	logger.Infow(ctx, "reenable-device", log.Fields{"device-id": device.Id})
	if handler := oo.getDeviceHandler(device.Id); handler != nil {
		if err := handler.ReenableDevice(log.WithSpanFromContext(context.Background(), ctx), device); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": device.Id}, nil)
}

//RebootDevice reboots the given device
func (oo *OpenOLT) RebootDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	logger.Infow(ctx, "reboot-device", log.Fields{"device-id": device.Id})
	if handler := oo.getDeviceHandler(device.Id); handler != nil {
		if err := handler.RebootDevice(log.WithSpanFromContext(context.Background(), ctx), device); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": device.Id}, nil)

}

//DeleteDevice deletes a device
func (oo *OpenOLT) DeleteDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	logger.Infow(ctx, "delete-device", log.Fields{"device-id": device.Id})
	if handler := oo.getDeviceHandler(device.Id); handler != nil {
		if err := handler.DeleteDevice(log.WithSpanFromContext(context.Background(), ctx), device); err != nil {
			logger.Errorw(ctx, "failed-to-handle-delete-device", log.Fields{"device-id": device.Id})
		}
		oo.deleteDeviceHandlerToMap(handler)
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": device.Id}, nil)
}

//UpdateFlowsIncrementally updates (add/remove) the flows on a given device
func (oo *OpenOLT) UpdateFlowsIncrementally(ctx context.Context, incrFlows *ca.IncrementalFlows) (*empty.Empty, error) {
	logger.Infow(ctx, "update_flows_incrementally", log.Fields{"device-id": incrFlows.Device.Id, "flows": incrFlows.Flows, "flowMetadata": incrFlows.FlowMetadata})
	if handler := oo.getDeviceHandler(incrFlows.Device.Id); handler != nil {
		if err := handler.UpdateFlowsIncrementally(log.WithSpanFromContext(context.Background(), ctx), incrFlows.Device, incrFlows.Flows, incrFlows.Groups, incrFlows.FlowMetadata); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": incrFlows.Device.Id}, nil)
}

//UpdatePmConfig returns PmConfigs nil or error
func (oo *OpenOLT) UpdatePmConfig(ctx context.Context, configs *ca.PmConfigsInfo) (*empty.Empty, error) {
	logger.Debugw(ctx, "update_pm_config", log.Fields{"device-id": configs.DeviceId, "pm-configs": configs.PmConfigs})
	if handler := oo.getDeviceHandler(configs.DeviceId); handler != nil {
		handler.UpdatePmConfig(log.WithSpanFromContext(context.Background(), ctx), configs.PmConfigs)
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": configs.DeviceId}, nil)
}

//SendPacketOut sends packet out to the device
func (oo *OpenOLT) SendPacketOut(ctx context.Context, packet *ca.PacketOut) (*empty.Empty, error) {
	logger.Debugw(ctx, "send_packet_out", log.Fields{"device-id": packet.DeviceId, "egress_port_no": packet.EgressPortNo, "pkt": packet.Packet})
	if handler := oo.getDeviceHandler(packet.DeviceId); handler != nil {
		if err := handler.PacketOut(log.WithSpanFromContext(context.Background(), ctx), packet.EgressPortNo, packet.Packet); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"device-id": packet.DeviceId}, nil)

}

// EnablePort to Enable PON/NNI interface
func (oo *OpenOLT) EnablePort(ctx context.Context, port *voltha.Port) (*empty.Empty, error) {
	logger.Infow(ctx, "enable_port", log.Fields{"device-id": port.DeviceId, "port": port})
	if err := oo.enableDisablePort(log.WithSpanFromContext(context.Background(), ctx), port.DeviceId, port, true); err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

// DisablePort to Disable pon/nni interface
func (oo *OpenOLT) DisablePort(ctx context.Context, port *voltha.Port) (*empty.Empty, error) {
	logger.Infow(ctx, "disable_port", log.Fields{"device-id": port.DeviceId, "port": port})
	if err := oo.enableDisablePort(log.WithSpanFromContext(context.Background(), ctx), port.DeviceId, port, false); err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

// enableDisablePort to Disable pon or Enable PON interface
func (oo *OpenOLT) enableDisablePort(ctx context.Context, deviceID string, port *voltha.Port, enablePort bool) error {
	logger.Infow(ctx, "enableDisablePort", log.Fields{"device-id": deviceID, "port": port})
	if port == nil {
		return olterrors.NewErrInvalidValue(log.Fields{
			"reason":    "port cannot be nil",
			"device-id": deviceID,
			"port":      nil}, nil)
	}
	if handler := oo.getDeviceHandler(deviceID); handler != nil {
		logger.Debugw(ctx, "Enable_Disable_Port", log.Fields{"device-id": deviceID, "port": port})
		if enablePort {
			if err := handler.EnablePort(ctx, port); err != nil {
				return olterrors.NewErrAdapter("error-occurred-during-enable-port", log.Fields{"device-id": deviceID, "port": port}, err)
			}
		} else {
			if err := handler.DisablePort(ctx, port); err != nil {
				return olterrors.NewErrAdapter("error-occurred-during-disable-port", log.Fields{"device-id": deviceID, "port": port}, err)
			}
		}
	}
	return nil
}

//ChildDeviceLost deletes the ONU and its references from PONResources
func (oo *OpenOLT) ChildDeviceLost(ctx context.Context, childDevice *voltha.Device) (*empty.Empty, error) {
	logger.Infow(ctx, "Child-device-lost", log.Fields{"parent-device-id": childDevice.ParentId, "child-device-id": childDevice.Id})
	if handler := oo.getDeviceHandler(childDevice.ParentId); handler != nil {
		if err := handler.ChildDeviceLost(log.WithSpanFromContext(context.Background(), ctx), childDevice.ParentPortNo, childDevice.ProxyAddress.OnuId, childDevice.SerialNumber); err != nil {
			return nil, err
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("device-handler", log.Fields{"parent-device-id": childDevice.ParentId}, nil).Log()
}

// GetExtValue retrieves a value on a particular ONU
func (oo *OpenOLT) GetExtValue(ctx context.Context, extInfo *ca.GetExtValueMessage) (*extension.ReturnValues, error) {
	var err error
	resp := new(extension.ReturnValues)
	logger.Infow(ctx, "get_ext_value", log.Fields{"parent-device-id": extInfo.ParentDevice.Id, "onu-id": extInfo.ChildDevice.Id})
	if handler := oo.getDeviceHandler(extInfo.ParentDevice.Id); handler != nil {
		if resp, err = handler.getExtValue(ctx, extInfo.ChildDevice, extInfo.ValueType); err != nil {
			logger.Errorw(ctx, "error-occurred-during-get-ext-value",
				log.Fields{"parent-device-id": extInfo.ParentDevice.Id, "onu-id": extInfo.ChildDevice.Id, "error": err})
			return nil, err
		}
	}
	return resp, nil
}

//GetSingleValue handles get uni status on ONU and ondemand metric on OLT
func (oo *OpenOLT) GetSingleValue(ctx context.Context, request *extension.SingleGetValueRequest) (*extension.SingleGetValueResponse, error) {
	logger.Infow(ctx, "single_get_value_request", log.Fields{"request": request})

	errResp := func(status extension.GetValueResponse_Status,
		reason extension.GetValueResponse_ErrorReason) *extension.SingleGetValueResponse {
		return &extension.SingleGetValueResponse{
			Response: &extension.GetValueResponse{
				Status:    status,
				ErrReason: reason,
			},
		}
	}
	if handler := oo.getDeviceHandler(request.TargetId); handler != nil {
		switch reqType := request.GetRequest().GetRequest().(type) {
		case *extension.GetValueRequest_OltPortInfo:
			return handler.getOltPortCounters(ctx, reqType.OltPortInfo), nil
		case *extension.GetValueRequest_OnuPonInfo:
			return handler.getOnuPonCounters(ctx, reqType.OnuPonInfo), nil
		case *extension.GetValueRequest_RxPower:
			return handler.getRxPower(ctx, reqType.RxPower), nil
		default:
			return errResp(extension.GetValueResponse_ERROR, extension.GetValueResponse_UNSUPPORTED), nil
		}
	}

	logger.Infow(ctx, "Single_get_value_request failed ", log.Fields{"request": request})
	return errResp(extension.GetValueResponse_ERROR, extension.GetValueResponse_INVALID_DEVICE_ID), nil
}

/*
 *  OLT Inter-adapter service
 */

// ProxyOmciRequest proxies an OMCI request from the child adapter
func (oo *OpenOLT) ProxyOmciRequest(ctx context.Context, request *ia.OmciMessage) (*empty.Empty, error) {
	logger.Debugw(ctx, "proxy-omci-request", log.Fields{"request": request})

	if handler := oo.getDeviceHandler(request.ParentDeviceId); handler != nil {
		if err := handler.ProxyOmciMessage(ctx, request); err != nil {
			return nil, errors.New(err.Error())
		}
		return &empty.Empty{}, nil
	}
	return nil, olterrors.NewErrNotFound("no-device-handler", log.Fields{"parent-device-id": request.ParentDeviceId, "child-device-id": request.ChildDeviceId}, nil).Log()
}

// GetTechProfileInstance returns an instance of a tech profile
func (oo *OpenOLT) GetTechProfileInstance(ctx context.Context, request *ia.TechProfileInstanceRequestMessage) (*ia.TechProfileDownloadMessage, error) {
	logger.Debugw(ctx, "getting-tech-profile-request", log.Fields{"request": request})

	targetDeviceID := request.ParentDeviceId
	if targetDeviceID == "" {
		return nil, olterrors.NewErrNotFound("parent-id-empty", log.Fields{"parent-device-id": request.ParentDeviceId, "child-device-id": request.DeviceId}, nil).Log()
	}
	if handler := oo.getDeviceHandler(targetDeviceID); handler != nil {
		return handler.GetTechProfileDownloadMessage(ctx, request)
	}
	return nil, olterrors.NewErrNotFound("no-device-handler", log.Fields{"parent-device-id": request.ParentDeviceId, "child-device-id": request.DeviceId}, nil).Log()

}

/*
 *
 * Unimplemented APIs
 *
 */

//SimulateAlarm is unimplemented
func (oo *OpenOLT) SimulateAlarm(context.Context, *ca.SimulateAlarmMessage) (*voltha.OperationResp, error) {
	return nil, olterrors.ErrNotImplemented
}

//SetExtValue is unimplemented
func (oo *OpenOLT) SetExtValue(context.Context, *ca.SetExtValueMessage) (*empty.Empty, error) {
	return nil, olterrors.ErrNotImplemented
}

//SetSingleValue is unimplemented
func (oo *OpenOLT) SetSingleValue(context.Context, *extension.SingleSetValueRequest) (*extension.SingleSetValueResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//StartOmciTest not implemented
func (oo *OpenOLT) StartOmciTest(ctx context.Context, test *ca.OMCITest) (*omci.TestResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//SuppressEvent unimplemented
func (oo *OpenOLT) SuppressEvent(ctx context.Context, filter *voltha.EventFilter) (*empty.Empty, error) {
	return nil, olterrors.ErrNotImplemented
}

//UnSuppressEvent  unimplemented
func (oo *OpenOLT) UnSuppressEvent(ctx context.Context, filter *voltha.EventFilter) (*empty.Empty, error) {
	return nil, olterrors.ErrNotImplemented
}

//DownloadImage is unimplemented
func (oo *OpenOLT) DownloadImage(ctx context.Context, imageInfo *ca.ImageDownloadMessage) (*voltha.ImageDownload, error) {
	return nil, olterrors.ErrNotImplemented
}

//GetImageDownloadStatus is unimplemented
func (oo *OpenOLT) GetImageDownloadStatus(ctx context.Context, imageInfo *ca.ImageDownloadMessage) (*voltha.ImageDownload, error) {
	return nil, olterrors.ErrNotImplemented
}

//CancelImageDownload is unimplemented
func (oo *OpenOLT) CancelImageDownload(ctx context.Context, imageInfo *ca.ImageDownloadMessage) (*voltha.ImageDownload, error) {
	return nil, olterrors.ErrNotImplemented
}

//ActivateImageUpdate is unimplemented
func (oo *OpenOLT) ActivateImageUpdate(ctx context.Context, imageInfo *ca.ImageDownloadMessage) (*voltha.ImageDownload, error) {
	return nil, olterrors.ErrNotImplemented
}

//RevertImageUpdate is unimplemented
func (oo *OpenOLT) RevertImageUpdate(ctx context.Context, imageInfo *ca.ImageDownloadMessage) (*voltha.ImageDownload, error) {
	return nil, olterrors.ErrNotImplemented
}

//DownloadOnuImage unimplemented
func (oo *OpenOLT) DownloadOnuImage(ctx context.Context, request *voltha.DeviceImageDownloadRequest) (*voltha.DeviceImageResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//GetOnuImageStatus unimplemented
func (oo *OpenOLT) GetOnuImageStatus(ctx context.Context, in *voltha.DeviceImageRequest) (*voltha.DeviceImageResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//AbortOnuImageUpgrade unimplemented
func (oo *OpenOLT) AbortOnuImageUpgrade(ctx context.Context, in *voltha.DeviceImageRequest) (*voltha.DeviceImageResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//GetOnuImages unimplemented
func (oo *OpenOLT) GetOnuImages(ctx context.Context, deviceID *common.ID) (*voltha.OnuImages, error) {
	return nil, olterrors.ErrNotImplemented
}

//ActivateOnuImage unimplemented
func (oo *OpenOLT) ActivateOnuImage(ctx context.Context, in *voltha.DeviceImageRequest) (*voltha.DeviceImageResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

//CommitOnuImage unimplemented
func (oo *OpenOLT) CommitOnuImage(ctx context.Context, in *voltha.DeviceImageRequest) (*voltha.DeviceImageResponse, error) {
	return nil, olterrors.ErrNotImplemented
}

// UpdateFlowsBulk is unimplemented
func (oo *OpenOLT) UpdateFlowsBulk(ctx context.Context, flows *ca.BulkFlows) (*empty.Empty, error) {
	return nil, olterrors.ErrNotImplemented
}

//SelfTestDevice unimplemented
func (oo *OpenOLT) SelfTestDevice(ctx context.Context, device *voltha.Device) (*empty.Empty, error) {
	return nil, olterrors.ErrNotImplemented
}
func (oo *OpenOLT) GetCustomVlan(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.GetVlanResponse, error) {
	var err error
	resp := new(bossopenolt.GetVlanResponse)
	deviceID := request.DeviceId
	logger.Infow(ctx, "Boss_get_vlan", log.Fields{"device-id": request.DeviceId, "request": request})
	if handler := oo.getDeviceHandler(deviceID); handler != nil {
		if resp, err = handler.BossGetVlan(ctx, request); err != nil {
			logger.Infow(ctx, "Boss_get_vlan_Error", log.Fields{"device-id": request.DeviceId, "request": request})

			return nil, err
		}
	}
	return resp, nil
}
func (oo *OpenOLT) GetOltConnect(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OltConnResponse, error) {
    var err error
    resp := new(bossopenolt.OltConnResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOltConnect", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOltConnect(ctx, request); err != nil {
            logger.Infow(ctx, "GetOltConnect_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetOltDeviceInfo(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OltDevResponse, error) {
    var err error
    resp := new(bossopenolt.OltDevResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOltDeviceInfo", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOltDeviceInfo(ctx, request); err != nil {
            logger.Infow(ctx, "GetOltDeviceInfo_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetPmdTxDis(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetPmdTxDis", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetPmdTxDis(ctx, request); err != nil {
            logger.Infow(ctx, "SetPmdTxDis_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetPmdTxdis(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.PmdTxdisResponse, error) {
    var err error
    resp := new(bossopenolt.PmdTxdisResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetPmdTxdis", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetPmdTxdis(ctx, request); err != nil {
            logger.Infow(ctx, "GetPmdTxdis_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetDevicePmdStatus(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.PmdStatusResponse, error) {
    var err error
    resp := new(bossopenolt.PmdStatusResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetDevicePmdStatus", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetDevicePmdStatus(ctx, request); err != nil {
            logger.Infow(ctx, "GetDevicePmdStatus_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetDevicePort(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetDevicePort", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetDevicePort(ctx, request); err != nil {
            logger.Infow(ctx, "SetDevicePort_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetDevicePort(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.GetPortResponse, error) {
    var err error
    resp := new(bossopenolt.GetPortResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetDevicePort", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetDevicePort(ctx, request); err != nil {
            logger.Infow(ctx, "GetDevicePort_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) PortReset(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "PortReset", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.PortReset(ctx, request); err != nil {
            logger.Infow(ctx, "PortReset_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetMtuSize(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetMtuSize", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetMtuSize(ctx, request); err != nil {
            logger.Infow(ctx, "SetMtuSize_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetMtuSize(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.MtuSizeResponse, error) {
    var err error
    resp := new(bossopenolt.MtuSizeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetMtuSize", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetMtuSize(ctx, request); err != nil {
            logger.Infow(ctx, "GetMtuSize_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetVlan(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetVlan", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetVlan(ctx, request); err != nil {
            logger.Infow(ctx, "SetVlan_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

/*func (oo *OpenOLT) GetVlan(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.GetVlanResponse, error) {
    var err error
    resp := new(bossopenolt.GetVlanResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetVlan", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetVlan(ctx, request); err != nil {
            logger.Infow(ctx, "GetVlan_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}*/

func (oo *OpenOLT) SetLutMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetLutMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetLutMode(ctx, request); err != nil {
            logger.Infow(ctx, "SetLutMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetLutMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ModeResponse, error) {
    var err error
    resp := new(bossopenolt.ModeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetLutMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetLutMode(ctx, request); err != nil {
            logger.Infow(ctx, "GetLutMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetAgingMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetAgingMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetAgingMode(ctx, request); err != nil {
            logger.Infow(ctx, "SetAgingMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetAgingMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ModeResponse, error) {
    var err error
    resp := new(bossopenolt.ModeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetAgingMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetAgingMode(ctx, request); err != nil {
            logger.Infow(ctx, "GetAgingMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetAgingTime(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetAgingTime", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetAgingTime(ctx, request); err != nil {
            logger.Infow(ctx, "SetAgingTime_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetAgingTime(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.AgingTimeResponse, error) {
    var err error
    resp := new(bossopenolt.AgingTimeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetAgingTime", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetAgingTime(ctx, request); err != nil {
            logger.Infow(ctx, "GetAgingTime_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetDeviceMacInfo(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.DevMacInfoResponse, error) {
    var err error
    resp := new(bossopenolt.DevMacInfoResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetDeviceMacInfo", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetDeviceMacInfo(ctx, request); err != nil {
            logger.Infow(ctx, "GetDeviceMacInfo_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetSdnTable(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.SdnTableKeyResponse, error) {
    var err error
    resp := new(bossopenolt.SdnTableKeyResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetSdnTable", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetSdnTable(ctx, request); err != nil {
            logger.Infow(ctx, "SetSdnTable_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetSdnTable(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.SdnTableResponse, error) {
    var err error
    resp := new(bossopenolt.SdnTableResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetSdnTable", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetSdnTable(ctx, request); err != nil {
            logger.Infow(ctx, "GetSdnTable_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetLength(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetLength", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetLength(ctx, request); err != nil {
            logger.Infow(ctx, "SetLength_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetLength(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.LengthResponse, error) {
    var err error
    resp := new(bossopenolt.LengthResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetLength", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetLength(ctx, request); err != nil {
            logger.Infow(ctx, "GetLength_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetQuietZone(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetQuietZone", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetQuietZone(ctx, request); err != nil {
            logger.Infow(ctx, "SetQuietZone_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetQuietZone(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.QuietZoneResponse, error) {
    var err error
    resp := new(bossopenolt.QuietZoneResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetQuietZone", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetQuietZone(ctx, request); err != nil {
            logger.Infow(ctx, "GetQuietZone_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetFecMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetFecMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetFecMode(ctx, request); err != nil {
            logger.Infow(ctx, "SetFecMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetFecMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ModeResponse, error) {
    var err error
    resp := new(bossopenolt.ModeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetFecMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetFecMode(ctx, request); err != nil {
            logger.Infow(ctx, "GetFecMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) AddOnu(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.AddOnuResponse, error) {
    var err error
    resp := new(bossopenolt.AddOnuResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "AddOnu", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.AddOnu(ctx, request); err != nil {
            logger.Infow(ctx, "AddOnu_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) DeleteOnu25G(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "DeleteOnu", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.DeleteOnu(ctx, request); err != nil {
            logger.Infow(ctx, "DeleteOnu_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) AddOnuSla(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "AddOnuSla", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.AddOnuSla(ctx, request); err != nil {
            logger.Infow(ctx, "AddOnuSla_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) ClearOnuSla(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "ClearOnuSla", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.ClearOnuSla(ctx, request); err != nil {
            logger.Infow(ctx, "ClearOnuSla_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetSlaTable(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.RepeatedSlaResponse, error) {
    var err error
    resp := new(bossopenolt.RepeatedSlaResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetSlaTable", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetSlaTable(ctx, request); err != nil {
            logger.Infow(ctx, "GetSlaTable_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetOnuAllocid(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetOnuAllocid", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetOnuAllocid(ctx, request); err != nil {
            logger.Infow(ctx, "SetOnuAllocid_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) DelOnuAllocid(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "DelOnuAllocid", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.DelOnuAllocid(ctx, request); err != nil {
            logger.Infow(ctx, "DelOnuAllocid_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetOnuVssn(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetOnuVssn", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetOnuVssn(ctx, request); err != nil {
            logger.Infow(ctx, "SetOnuVssn_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetOnuVssn(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OnuVssnResponse, error) {
    var err error
    resp := new(bossopenolt.OnuVssnResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOnuVssn", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOnuVssn(ctx, request); err != nil {
            logger.Infow(ctx, "GetOnuVssn_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetOnuDistance(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OnuDistResponse, error) {
    var err error
    resp := new(bossopenolt.OnuDistResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOnuDistance", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOnuDistance(ctx, request); err != nil {
            logger.Infow(ctx, "GetOnuDistance_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetBurstDelimiter(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetBurstDelimiter", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetBurstDelimiter(ctx, request); err != nil {
            logger.Infow(ctx, "SetBurstDelimiter_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetBurstDelimiter(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BurstDelimitResponse, error) {
    var err error
    resp := new(bossopenolt.BurstDelimitResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetBurstDelimiter", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetBurstDelimiter(ctx, request); err != nil {
            logger.Infow(ctx, "GetBurstDelimiter_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetBurstPreamble(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetBurstPreamble", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetBurstPreamble(ctx, request); err != nil {
            logger.Infow(ctx, "SetBurstPreamble_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetBurstPreamble(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BurstPreambleResponse, error) {
    var err error
    resp := new(bossopenolt.BurstPreambleResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetBurstPreamble", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetBurstPreamble(ctx, request); err != nil {
            logger.Infow(ctx, "GetBurstPreamble_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetBurstVersion(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetBurstVersion", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetBurstVersion(ctx, request); err != nil {
            logger.Infow(ctx, "SetBurstVersion_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetBurstVersion(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BurstVersionResponse, error) {
    var err error
    resp := new(bossopenolt.BurstVersionResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetBurstVersion", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetBurstVersion(ctx, request); err != nil {
            logger.Infow(ctx, "GetBurstVersion_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetBurstProfile(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetBurstProfile", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetBurstProfile(ctx, request); err != nil {
            logger.Infow(ctx, "SetBurstProfile_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetBurstProfile(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BurstProfileResponse, error) {
    var err error
    resp := new(bossopenolt.BurstProfileResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetBurstProfile", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetBurstProfile(ctx, request); err != nil {
            logger.Infow(ctx, "GetBurstProfile_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetRegisterStatus(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.RegisterStatusResponse, error) {
    var err error
    resp := new(bossopenolt.RegisterStatusResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetRegisterStatus", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetRegisterStatus(ctx, request); err != nil {
            logger.Infow(ctx, "GetRegisterStatus_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetOnuInfo(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OnuInfoResponse, error) {
    var err error
    resp := new(bossopenolt.OnuInfoResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOnuInfo", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOnuInfo(ctx, request); err != nil {
            logger.Infow(ctx, "GetOnuInfo_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetOmciStatus(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.StatusResponse, error) {
    var err error
    resp := new(bossopenolt.StatusResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetOmciStatus", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetOmciStatus(ctx, request); err != nil {
            logger.Infow(ctx, "GetOmciStatus_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetDsOmciOnu(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetDsOmciOnu", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetDsOmciOnu(ctx, request); err != nil {
            logger.Infow(ctx, "SetDsOmciOnu_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetDsOmciData(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetDsOmciData", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetDsOmciData(ctx, request); err != nil {
            logger.Infow(ctx, "SetDsOmciData_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetUsOmciData(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.OmciDataResponse, error) {
    var err error
    resp := new(bossopenolt.OmciDataResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetUsOmciData", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetUsOmciData(ctx, request); err != nil {
            logger.Infow(ctx, "GetUsOmciData_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetTod(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetTod", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetTod(ctx, request); err != nil {
            logger.Infow(ctx, "SetTod_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetTod(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.TodResponse, error) {
    var err error
    resp := new(bossopenolt.TodResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetTod", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetTod(ctx, request); err != nil {
            logger.Infow(ctx, "GetTod_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetDataMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetDataMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetDataMode(ctx, request); err != nil {
            logger.Infow(ctx, "SetDataMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetDataMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ModeResponse, error) {
    var err error
    resp := new(bossopenolt.ModeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetDataMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetDataMode(ctx, request); err != nil {
            logger.Infow(ctx, "GetDataMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetFecDecMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetFecDecMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetFecDecMode(ctx, request); err != nil {
            logger.Infow(ctx, "SetFecDecMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetFecDecMode(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ModeResponse, error) {
    var err error
    resp := new(bossopenolt.ModeResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetFecDecMode", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetFecDecMode(ctx, request); err != nil {
            logger.Infow(ctx, "GetFecDecMode_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetDelimiter(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetDelimiter", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetDelimiter(ctx, request); err != nil {
            logger.Infow(ctx, "SetDelimiter_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetDelimiter(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.FecDecResponse, error) {
    var err error
    resp := new(bossopenolt.FecDecResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetDelimiter", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetDelimiter(ctx, request); err != nil {
            logger.Infow(ctx, "GetDelimiter_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetErrorPermit(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetErrorPermit", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetErrorPermit(ctx, request); err != nil {
            logger.Infow(ctx, "SetErrorPermit_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetErrorPermit(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ErrorPermitResponse, error) {
    var err error
    resp := new(bossopenolt.ErrorPermitResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetErrorPermit", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetErrorPermit(ctx, request); err != nil {
            logger.Infow(ctx, "GetErrorPermit_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetPmControl(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetPmControl", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetPmControl(ctx, request); err != nil {
            logger.Infow(ctx, "SetPmControl_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetPmControl(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.PmControlResponse, error) {
    var err error
    resp := new(bossopenolt.PmControlResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetPmControl", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetPmControl(ctx, request); err != nil {
            logger.Infow(ctx, "GetPmControl_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetPmTable(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.PmTableResponse, error) {
    var err error
    resp := new(bossopenolt.PmTableResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetPmTable", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetPmTable(ctx, request); err != nil {
            logger.Infow(ctx, "GetPmTable_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetSAOn(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetSAOn", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetSAOn(ctx, request); err != nil {
            logger.Infow(ctx, "SetSAOn_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetSAOff(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetSAOff", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetSAOff(ctx, request); err != nil {
            logger.Infow(ctx, "SetSAOff_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) CreateDeviceHandler(ctx context.Context, device *voltha.Device)(*empty.Empty, error) {
	if device == nil {
		return nil, olterrors.NewErrInvalidValue(log.Fields{"device": nil}, nil).Log()
	}
	logger.Infow(ctx, "Create_device_handler", log.Fields{"device-id": device.Id})
	var handler *DeviceHandler
	if handler = oo.getDeviceHandler(device.Id); handler == nil {
		//handler := NewDeviceHandler(oo.coreProxy, oo.adapterProxy, oo.eventProxy, device, oo, oo.configManager)
		handler := NewDeviceHandler(oo.coreClient, oo.eventProxy, device, oo, oo.configManager, oo.config)

		oo.addDeviceHandlerToMap(handler)
		go handler.CreateDeviceHandler(ctx,device)
//			go handler.AdoptDevice(ctx, device)
		// Launch the creation of the device topic
		// go oo.createDeviceTopic(device)
	}
	return &empty.Empty{},nil
}
func (oo *OpenOLT) SetSliceBw(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.ExecResult, error) {
    var err error
    resp := new(bossopenolt.ExecResult)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetSliceBw", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetSliceBw(ctx, request); err != nil {
            logger.Infow(ctx, "SetSliceBw_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetSliceBw(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.GetSliceBwResponse, error) {
    var err error
    resp := new(bossopenolt.GetSliceBwResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetSliceBw", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetSliceBw(ctx, request); err != nil {
            logger.Infow(ctx, "GetSliceBw_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) SetSlaV2(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.RepeatedSlaV2Response, error) {
    var err error
    resp := new(bossopenolt.RepeatedSlaV2Response)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SetSlaV2", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SetSlaV2(ctx, request); err != nil {
            logger.Infow(ctx, "SetSlaV2_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

func (oo *OpenOLT) GetSlaV2(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.RepeatedSlaV2Response, error) {
    var err error
    resp := new(bossopenolt.RepeatedSlaV2Response)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetSlaV2", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetSlaV2(ctx, request); err != nil {
            logger.Infow(ctx, "GetSlaV2_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}
func (oo *OpenOLT) SendOmciData(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BossOmciResponse, error) {
    var err error
    resp := new(bossopenolt.BossOmciResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "SendOmciData", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.SendOmciData(ctx, request); err != nil {
            logger.Infow(ctx, "SendOmciData_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}
func (oo *OpenOLT) SendActiveOnu(ctx context.Context, device *voltha.ActiveOnu)(*empty.Empty, error) {
  deviceId := device.DeviceId
  logger.Infow(ctx, "SendActiveOnu", log.Fields{"device id": device.DeviceId, "request": device})
  if handler := oo.getDeviceHandler(deviceId); handler!=nil{
    logger.Infow(ctx, "SendActiveOnu Device Handler is Not NULL", log.Fields{"device id": device.DeviceId, "request": device})
    if err:= handler.SendActiveOnu(ctx, device); err!=nil{
      return nil, err
    }
  }else{
    logger.Infow(ctx, "SendActiveOnu Device Handler is NULL", log.Fields{"device id": device.DeviceId, "request": device})
  }
	return &empty.Empty{},nil
}

func (oo *OpenOLT) SendOmciDatav2(ctx context.Context, msg *voltha.OmciDatav2)(*empty.Empty, error){
  deviceId := msg.DeviceId
  logger.Infow(ctx, "SendOmciDatav2", log.Fields{"device id": msg.DeviceId, "request": msg})
  if handler := oo.getDeviceHandler(deviceId); handler!=nil{
    logger.Infow(ctx, "SendOmciDatav2 Device Handler is Not NULL", log.Fields{"device id": msg.DeviceId, "request": msg})
    if err:= handler.SendOmciDatav2(ctx, msg); err!=nil{
      return nil, err
    }
  }else{
    logger.Infow(ctx, "SendOmciDatav2 Device Handler is NULL", log.Fields{"device id": msg.DeviceId, "request": msg})
    return nil,olterrors.ErrNotImplemented
  }
	return &empty.Empty{},nil

}
func (oo *OpenOLT) GetEtcdList(ctx context.Context, id *voltha.ID)(*voltha.EtcdList, error){
  deviceId := id.Id
  logger.Infow(ctx, "GetEtcdList", log.Fields{"device id": id})
  if handler := oo.getDeviceHandler(strings.Replace(fmt.Sprintf("%s",deviceId),"\\","",-1)); handler!=nil{
  //if handler := oo.getDeviceHandler(strings.Replace(fmt.Sprintf("%s",deviceId),"\\","",-1)); handler!=nil{
    logger.Infow(ctx, "GetEtcdList Device Handler is Not NULL", log.Fields{"device id": id})
    return handler.getEtcdList(ctx);
  }else{
    logger.Infow(ctx, "GetEtcdList Device Handler is NULL", log.Fields{"device id": id})
    return nil,olterrors.ErrNotImplemented
  }

}
func (oo *OpenOLT) GetPktInd(ctx context.Context, request *bossopenolt.BossRequest) (*bossopenolt.BossPktIndResponse, error) {
    var err error
    resp := new(bossopenolt.BossPktIndResponse)
    deviceID := request.DeviceId
    logger.Infow(ctx, "GetPktInd", log.Fields{"device-id": request.DeviceId, "request": request})
    if handler := oo.getDeviceHandler(deviceID); handler != nil {
        if resp, err = handler.GetPktInd(ctx, request); err != nil {
            logger.Infow(ctx, "GetPktInd_Error", log.Fields{"device-id": request.DeviceId, "request": request})

            return nil, err
        }
    }
    return resp, nil
}

