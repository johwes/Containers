# Documentation: https://hub.docker.com/_/rabbitmq/

FROM rabbitmq:3.7-management-alpine

 

RUN mkdir -p /data/mnesia && \
    mkdir -p /data/logs && \
    chown -R rabbitmq:0 /data && \
    chmod -R g=u /data
 

env RABBITMQ_MNESIA_BASE /data/mnesia
env RABBITMQ_IO_THREAD_POOL_SIZE=128
env RABBITMQ_LOG_BASE /data/logs

 

RUN rm -f /etc/rabbitmq/rabbitmq.conf

ADD rabbitmq.conf /etc/rabbitmq/rabbitmq.conf

 

EXPOSE 5672

EXPOSE 15672
