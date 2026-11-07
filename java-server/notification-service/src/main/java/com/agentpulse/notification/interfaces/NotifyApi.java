package com.agentpulse.notification.interfaces;

import com.agentpulse.notification.application.Notify;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.sun.net.httpserver.HttpExchange;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.util.Map;
import java.util.Optional;

/** {@code POST /notify}: an agent asks the service to notify people. It decodes, validates and delivers. */
final class NotifyApi {
    private static final Logger LOG = LoggerFactory.getLogger(NotifyApi.class);

    private final ApiSupport api;
    private final Notify.Service service;

    NotifyApi(ApiSupport api, Notify.Service service) {
        this.api = api;
        this.service = service;
    }

    void handle(HttpExchange x) throws IOException {
        if (!api.authorised(x)) {
            return;
        }
        if (!"POST".equals(x.getRequestMethod())) {
            api.error(x, 405, "method not allowed");
            return;
        }
        Optional<JsonNode> body = api.readJson(x);
        if (body.isEmpty()) {
            return;
        }
        Notify.Request request;
        try {
            // JSON null is an empty request, which then fails validation with a clear message (as in the Go service).
            request = body.get().isNull() ? new Notify.Request(null, null, null, null, null, null, null)
                    : ApiSupport.JSON.treeToValue(body.get(), Notify.Request.class);
        } catch (JsonProcessingException e) {
            api.error(x, 400, "invalid request payload");
            return;
        }
        try {
            service.send(request);
        } catch (Notify.InvalidRequestException e) {
            api.error(x, 400, e.getMessage());
            return;
        } catch (RuntimeException e) {
            LOG.error("send notification failed error={}", e.getMessage());
            api.error(x, 500, "internal error");
            return;
        }
        api.reply(x, 202, Map.of("status", "accepted"));
    }
}
