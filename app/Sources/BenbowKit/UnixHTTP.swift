// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Darwin
import Foundation

/// A minimal HTTP/1.0 client over a Unix socket. URLSession cannot dial Unix
/// sockets; HTTP/1.0 keeps the exchange trivial: one request per connection,
/// no chunked encoding, the body ends when the daemon closes the socket.
public struct UnixHTTP: Sendable {
    public let socketPath: String
    /// How long to wait for an answer. Installing PHP takes minutes.
    public var timeout: TimeInterval = 15 * 60

    public init(socketPath: String) {
        self.socketPath = socketPath
    }

    public struct Response: Sendable {
        public let status: Int
        public let body: Data
    }

    public enum Failure: Error, Equatable {
        case pathTooLong
        case connect(Int32)
        case io(Int32)
        case malformed
    }

    public func send(method: String, path: String, body: Data? = nil) async throws -> Response {
        let socketPath = socketPath, timeout = timeout
        return try await Task.detached {
            try Self.roundTrip(socketPath: socketPath, timeout: timeout, method: method, path: path, body: body)
        }.value
    }

    static func roundTrip(socketPath: String, timeout: TimeInterval, method: String, path: String, body: Data?) throws -> Response {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw Failure.connect(errno) }
        defer { close(fd) }

        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        var tv = timeval(tv_sec: Int(timeout), tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(socketPath.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { throw Failure.pathTooLong }
        withUnsafeMutableBytes(of: &addr.sun_path) { buf in
            buf.copyBytes(from: bytes)
            buf[bytes.count] = 0
        }
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0 else { throw Failure.connect(errno) }

        var head = "\(method) \(path) HTTP/1.0\r\nHost: benbow\r\n"
        if let body {
            head += "Content-Type: application/json\r\nContent-Length: \(body.count)\r\n"
        }
        head += "\r\n"
        var request = Data(head.utf8)
        if let body { request.append(body) }
        try request.withUnsafeBytes { raw in
            var sent = 0
            while sent < raw.count {
                let n = write(fd, raw.baseAddress! + sent, raw.count - sent)
                if n < 0 {
                    if errno == EINTR { continue }
                    throw Failure.io(errno)
                }
                sent += n
            }
        }

        var response = Data()
        var buf = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let n = read(fd, &buf, buf.count)
            if n < 0 {
                if errno == EINTR { continue }
                throw Failure.io(errno)
            }
            if n == 0 { break }
            response.append(buf, count: n)
        }
        return try parse(response)
    }

    static func parse(_ raw: Data) throws -> Response {
        guard let sep = raw.range(of: Data("\r\n\r\n".utf8)),
              let head = String(data: raw[..<sep.lowerBound], encoding: .utf8),
              let statusLine = head.split(separator: "\r\n").first
        else { throw Failure.malformed }
        let parts = statusLine.split(separator: " ")
        guard parts.count >= 2, parts[0].hasPrefix("HTTP/"), let status = Int(parts[1]) else {
            throw Failure.malformed
        }
        return Response(status: status, body: Data(raw[sep.upperBound...]))
    }
}
