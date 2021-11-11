FROM registry.access.redhat.com/ubi8/php-74 

# Add application sources to a directory that the assemble script expects them 
# and set permissions so that the container runs without root access 
USER 0
#ADD src /tmp/src 
RUN git clone https://github.com/phpipam/phpipam.git /tmp/src && \ 
    cd /tmp/src && \
    git checkout 1.4 && \
    git submodule update --init --recursive && \
    cp /tmp/src/config.docker.php /tmp/src/config.php && \
    chown -R 1001:0 /tmp/src && \
    yum install -y php-pear && yum clean all

USER 1001 

# Install the dependencies 
RUN /usr/libexec/s2i/assemble 

# Set the default command for the resulting image 
CMD /usr/libexec/s2i/run
