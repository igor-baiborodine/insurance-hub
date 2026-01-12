package pl.altkom.asc.lab.micronaut.poc.gateway.infrastructure;

import io.micronaut.http.HttpRequest;
import io.micronaut.http.MutableHttpResponse;
import io.micronaut.http.annotation.Filter;
import io.micronaut.http.filter.HttpServerFilter;
import io.micronaut.http.filter.ServerFilterChain;
import lombok.extern.slf4j.Slf4j;
import org.reactivestreams.Publisher;
import io.reactivex.Flowable;

@Slf4j
@Filter("/**")
public class LoggingFilter implements HttpServerFilter {

    @Override
    public Publisher<MutableHttpResponse<?>> doFilter(HttpRequest<?> request, ServerFilterChain chain) {
        if (request.getPath().equals("/health")) {
            return chain.proceed(request);
        }
        log.info("Incoming Request: {} {}", request.getMethod(), request.getUri());

        return Flowable.fromPublisher(chain.proceed(request))
                .doOnNext(response -> {
                    log.info("Response for {} {}: Status {}",
                            request.getMethod(), request.getUri(), response.getStatus());
                });
    }
}