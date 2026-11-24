package pl.altkom.asc.lab.micronaut.poc.gateway.infrastructure;

import com.fasterxml.jackson.databind.node.JsonNodeFactory;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.micronaut.core.convert.exceptions.ConversionErrorException;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.HttpStatus;
import io.micronaut.http.MediaType;
import io.micronaut.http.MutableHttpResponse;
import io.micronaut.http.annotation.Filter;
import io.micronaut.http.client.exceptions.HttpClientException;
import io.micronaut.http.client.exceptions.HttpClientResponseException;
import io.micronaut.http.codec.CodecException;
import io.micronaut.http.exceptions.HttpStatusException;
import io.micronaut.http.filter.HttpServerFilter;
import io.micronaut.http.filter.ServerFilterChain;
import io.micronaut.security.authentication.AuthorizationException;
import io.micronaut.web.router.exceptions.UnsatisfiedRouteException;
import io.reactivex.Flowable;
import org.reactivestreams.Publisher;

/**
 * Keeps the Policy lifecycle statuses and replaces every error body with the sanitized envelope.
 * The pattern matches lookup, create and terminate, but not the policy search route.
 */
@Filter("/api/policies/*")
public class PolicyLifecycleErrorFilter implements HttpServerFilter {

    @Override
    public Publisher<MutableHttpResponse<?>> doFilter(HttpRequest<?> request, ServerFilterChain chain) {
        return Flowable.fromPublisher(chain.proceed(request))
                .map(this::sanitizeResponse)
                .onErrorReturn(this::sanitizeFailure);
    }

    private MutableHttpResponse<?> sanitizeResponse(MutableHttpResponse<?> response) {
        HttpStatus status = response.getStatus();
        if (status == HttpStatus.UNAUTHORIZED || status == HttpStatus.FORBIDDEN
                || status.getCode() < 400) {
            return response;
        }
        return safeResponse(preservedStatus(status));
    }

    private MutableHttpResponse<?> sanitizeFailure(Throwable error) {
        if (error instanceof AuthorizationException) {
            AuthorizationException authorization = (AuthorizationException) error;
            return HttpResponse.status(authorization.isForbidden()
                    ? HttpStatus.FORBIDDEN : HttpStatus.UNAUTHORIZED);
        }
        if (error instanceof HttpClientResponseException) {
            return safeResponse(preservedStatus(((HttpClientResponseException) error).getStatus()));
        }
        if (error instanceof HttpClientException) {
            return safeResponse(HttpStatus.SERVICE_UNAVAILABLE);
        }
        if (error instanceof ConversionErrorException || error instanceof CodecException
                || error instanceof UnsatisfiedRouteException) {
            return safeResponse(HttpStatus.BAD_REQUEST);
        }
        if (error instanceof HttpStatusException) {
            return safeResponse(preservedStatus(((HttpStatusException) error).getStatus()));
        }
        return safeResponse(HttpStatus.INTERNAL_SERVER_ERROR);
    }

    private HttpStatus preservedStatus(HttpStatus status) {
        switch (status) {
            case BAD_REQUEST:
            case NOT_FOUND:
            case METHOD_NOT_ALLOWED:
            case CONFLICT:
            case UNSUPPORTED_MEDIA_TYPE:
            case SERVICE_UNAVAILABLE:
            case GATEWAY_TIMEOUT:
                return status;
            default:
                return HttpStatus.INTERNAL_SERVER_ERROR;
        }
    }

    private MutableHttpResponse<?> safeResponse(HttpStatus status) {
        return HttpResponse.status(status)
                .contentType(MediaType.APPLICATION_JSON_TYPE)
                .body(safeBody(status));
    }

    private ObjectNode safeBody(HttpStatus status) {
        int code;
        String message;
        switch (status) {
            case BAD_REQUEST:
                code = 3;
                message = "invalid request";
                break;
            case NOT_FOUND:
                code = 5;
                message = "not found";
                break;
            case METHOD_NOT_ALLOWED:
                code = 12;
                message = "method not allowed";
                break;
            case CONFLICT:
                code = 9;
                message = "conflict";
                break;
            case UNSUPPORTED_MEDIA_TYPE:
                code = 3;
                message = "unsupported media type";
                break;
            case SERVICE_UNAVAILABLE:
                code = 14;
                message = "service unavailable";
                break;
            case GATEWAY_TIMEOUT:
                code = 4;
                message = "deadline exceeded";
                break;
            default:
                code = 13;
                message = "internal error";
        }
        ObjectNode body = JsonNodeFactory.instance.objectNode();
        body.put("code", code);
        body.put("message", message);
        body.putArray("details");
        return body;
    }
}
