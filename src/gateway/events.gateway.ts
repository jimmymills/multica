import { Injectable } from "@nestjs/common";
import {
  WebSocketGateway,
  WebSocketServer,
  SubscribeMessage,
  MessageBody,
  ConnectedSocket,
} from "@nestjs/websockets";
import type {
  OnGatewayInit,
  OnGatewayConnection,
  OnGatewayDisconnect,
} from "@nestjs/websockets";
import type { Server, Socket } from "socket.io";
import { InjectPinoLogger, PinoLogger } from "nestjs-pino";
import {
  GatewayEvents,
  type RoutedMessage,
  type SendErrorResponse,
  type PingPayload,
  type PongResponse,
  type DeviceInfo,
  type DeviceType,
} from "@multica/sdk";

@Injectable()
@WebSocketGateway({
  path: "/ws",
  cors: {
    origin: "*",
  },
  // Heartbeat detection configuration
  pingInterval: 25000, // Send PING every 25 seconds
  pingTimeout: 20000, // Must respond within 20 seconds, otherwise disconnect
})
export class EventsGateway
  implements OnGatewayInit, OnGatewayConnection, OnGatewayDisconnect
{
  constructor(
    @InjectPinoLogger(EventsGateway.name)
    private readonly logger: PinoLogger
  ) {}

  @WebSocketServer()
  server!: Server;

  // deviceId -> socketId mapping
  private deviceToSocket = new Map<string, string>();
  // socketId -> deviceInfo mapping
  private socketToDevice = new Map<string, DeviceInfo>();

  afterInit(_server: Server): void {
    this.logger.info("WebSocket Gateway initialized");
  }

  handleConnection(client: Socket): void {
    const query = client.handshake.query;
    const deviceId = query["deviceId"] as string | undefined;
    const deviceType = query["deviceType"] as DeviceType | undefined;

    this.logger.debug(
      { socketId: client.id, deviceId, deviceType },
      "Incoming connection"
    );

    if (!deviceId || !deviceType) {
      this.logger.warn(
        { socketId: client.id },
        "Missing deviceId or deviceType in query, disconnecting"
      );
      client.disconnect(true);
      return;
    }

    // Check if deviceId is already in use by another socket
    const existingSocketId = this.deviceToSocket.get(deviceId);
    if (existingSocketId && existingSocketId !== client.id) {
      this.logger.warn(
        { deviceId, existingSocketId },
        "Device already registered by another socket, disconnecting"
      );
      client.emit(GatewayEvents.REGISTERED, {
        success: false,
        deviceId,
        error: "Device ID already in use",
      });
      client.disconnect(true);
      return;
    }

    // Register device
    const deviceInfo: DeviceInfo = { deviceId, deviceType };
    this.deviceToSocket.set(deviceId, client.id);
    this.socketToDevice.set(client.id, deviceInfo);

    this.logger.info({ deviceId, deviceType }, "Device connected and registered");
    client.emit(GatewayEvents.REGISTERED, { success: true, deviceId });
  }

  handleDisconnect(client: Socket): void {
    const deviceInfo = this.socketToDevice.get(client.id);
    if (deviceInfo) {
      this.logger.debug(
        { socketId: client.id, deviceId: deviceInfo.deviceId, deviceType: deviceInfo.deviceType },
        "Device disconnecting"
      );
      this.logger.info(
        { deviceId: deviceInfo.deviceId, deviceType: deviceInfo.deviceType },
        "Device disconnected"
      );
      this.deviceToSocket.delete(deviceInfo.deviceId);
      this.socketToDevice.delete(client.id);
    } else {
      this.logger.info({ socketId: client.id }, "Socket disconnected");
    }
  }

  @SubscribeMessage(GatewayEvents.SEND)
  handleSend(
    @MessageBody() message: RoutedMessage,
    @ConnectedSocket() client: Socket
  ): void {
    this.logger.debug(
      { socketId: client.id, message },
      "Received send event"
    );

    const senderDevice = this.socketToDevice.get(client.id);

    // Check if sender is registered
    if (!senderDevice) {
      const error: SendErrorResponse = {
        messageId: message.id,
        error: "Sender not registered",
        code: "NOT_REGISTERED",
      };
      client.emit(GatewayEvents.SEND_ERROR, error);
      return;
    }

    // Check if message 'from' matches
    if (message.from !== senderDevice.deviceId) {
      const error: SendErrorResponse = {
        messageId: message.id,
        error: "Invalid sender ID",
        code: "INVALID_MESSAGE",
      };
      client.emit(GatewayEvents.SEND_ERROR, error);
      return;
    }

    // Find target device
    const targetSocketId = this.deviceToSocket.get(message.to);
    if (!targetSocketId) {
      const error: SendErrorResponse = {
        messageId: message.id,
        error: `Device ${message.to} not found`,
        code: "DEVICE_NOT_FOUND",
      };
      client.emit(GatewayEvents.SEND_ERROR, error);
      return;
    }

    // Forward message
    this.logger.debug(
      { messageId: message.id, from: message.from, to: message.to, action: message.action },
      "Routing message"
    );
    this.server.to(targetSocketId).emit(GatewayEvents.RECEIVE, message);
  }

  @SubscribeMessage(GatewayEvents.PING)
  handlePing(
    @MessageBody() data: PingPayload,
    @ConnectedSocket() client: Socket
  ): PongResponse {
    this.logger.debug({ socketId: client.id, data }, "Received ping");
    return { event: GatewayEvents.PONG, data: "Hello from Gateway!" };
  }

  /** Get all online devices (for HTTP API use) */
  getOnlineDevices(): DeviceInfo[] {
    return Array.from(this.socketToDevice.values());
  }

  /** Get online devices of specified type */
  getOnlineDevicesByType(type: "client" | "agent"): DeviceInfo[] {
    return this.getOnlineDevices().filter((d) => d.deviceType === type);
  }

  /** Send message to specified device (for HTTP API use) */
  sendToDevice(deviceId: string, event: string, data: unknown): boolean {
    const socketId = this.deviceToSocket.get(deviceId);
    if (!socketId) return false;
    this.server.to(socketId).emit(event, data);
    return true;
  }
}
